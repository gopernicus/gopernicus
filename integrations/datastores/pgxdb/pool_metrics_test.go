package pgxdb

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/metrics"
)

// gaugeMeter records registered gauges; failAt makes the Nth Gauge fail.
type gaugeMeter struct {
	metrics.Noop
	mu         sync.Mutex
	gauges     map[string]func(context.Context) []metrics.Observation
	unregs     int
	failAt     int
	registered int
}

func (m *gaugeMeter) Gauge(spec metrics.Spec, observe func(context.Context) []metrics.Observation) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered++
	if m.failAt > 0 && m.registered == m.failAt {
		return func() {}, errors.New("refused")
	}
	if m.gauges == nil {
		m.gauges = map[string]func(context.Context) []metrics.Observation{}
	}
	m.gauges[spec.Name] = observe
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.gauges[spec.Name]; ok {
			delete(m.gauges, spec.Name)
			m.unregs++
		}
	}, nil
}

func (m *gaugeMeter) names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.gauges {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestRegisterPoolMetricsNamesAndRollback(t *testing.T) {
	m := &gaugeMeter{}
	unregister, err := (&DB{}).RegisterPoolMetrics(m, PoolMetricsConfig{PoolName: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	want := "db.client.connection.acquire.canceled,db.client.connection.acquire.count,db.client.connection.acquire.duration,db.client.connection.acquire.empty,db.client.connection.constructing,db.client.connection.count,db.client.connection.create.count,db.client.connection.destroy.count,db.client.connection.max,db.client.connection.wait.duration"
	if got := strings.Join(m.names(), ","); got != want {
		t.Fatalf("gauges = %s", got)
	}
	obs := m.gauges["db.client.connection.count"](context.Background())
	if len(obs) != 2 {
		t.Fatalf("count observations = %+v", obs)
	}
	for _, o := range obs {
		labels := map[string]string{}
		for _, a := range o.Attrs {
			labels[a.Key] = a.Value
		}
		if labels["db.system.name"] != "postgresql" || labels["db.client.connection.pool.name"] != "primary" || (labels["db.client.connection.state"] != "idle" && labels["db.client.connection.state"] != "used") {
			t.Fatalf("labels = %v", labels)
		}
	}
	unregister()
	unregister()
	if len(m.names()) != 0 || m.unregs != 10 {
		t.Fatalf("after unregister: %v (%d unregistered)", m.names(), m.unregs)
	}

	// A failing registration unregisters the ones before it.
	failing := &gaugeMeter{failAt: 4}
	if _, err := (&DB{}).RegisterPoolMetrics(failing, PoolMetricsConfig{}); err == nil {
		t.Fatal("a failed gauge registration returned no error")
	}
	if len(failing.names()) != 0 || failing.unregs != 3 {
		t.Fatalf("rollback left %v", failing.names())
	}

	// A nil meter or DB registers nothing.
	if u, err := (&DB{}).RegisterPoolMetrics(nil, PoolMetricsConfig{}); err != nil || u == nil {
		t.Fatal("nil meter")
	}
}

// TestLive_RegisterPoolMetrics: the gauges read the live pool's Stats on each
// collection.
func TestLive_RegisterPoolMetrics(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — pool metrics NOT verified live")
	}
	db, err := Open(context.Background(), Config{DSN: sdk.Secret(dsn), MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := &gaugeMeter{}
	unregister, err := db.RegisterPoolMetrics(m, PoolMetricsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	read := func(name string) float64 {
		return m.gauges[name](context.Background())[0].Value
	}
	before := read("db.client.connection.acquire.count")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		var one int
		if err := db.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
			t.Fatal(err)
		}
	}
	if after := read("db.client.connection.acquire.count"); after < before+3 {
		t.Fatalf("acquire.count %v → %v, want +3", before, after)
	}
	if read("db.client.connection.max") != 4 {
		t.Fatalf("max = %v", read("db.client.connection.max"))
	}
}
