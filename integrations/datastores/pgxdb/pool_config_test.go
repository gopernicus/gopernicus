package pgxdb

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPoolConfigPreservesParsedDefaults(t *testing.T) {
	for _, dsn := range []string{
		testDSN,
		testDSN + "&pool_max_conn_lifetime=17m&pool_max_conn_idle_time=3m&pool_max_conns=7&pool_min_conns=2",
	} {
		want, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		got, err := (Config{}).poolConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if got.MaxConnLifetime != want.MaxConnLifetime || got.MaxConnIdleTime != want.MaxConnIdleTime ||
			got.MaxConns != want.MaxConns || got.MinConns != want.MinConns || got.HealthCheckPeriod != want.HealthCheckPeriod {
			t.Fatalf("omitted pool settings changed parsed defaults: lifetime=%v idle=%v max=%d min=%d health=%v", got.MaxConnLifetime, got.MaxConnIdleTime, got.MaxConns, got.MinConns, got.HealthCheckPeriod)
		}
	}
}

func TestPoolConfigExplicitOverrides(t *testing.T) {
	cfg := Config{MaxConns: 4, MinConns: 1, MaxLifetime: time.Hour, MaxIdleTime: time.Minute, HealthCheckPeriod: 10 * time.Second}
	got, err := cfg.poolConfig(testDSN + "&pool_max_conn_lifetime=17m&pool_max_conn_idle_time=3m&pool_max_conns=7&pool_min_conns=2")
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxConnLifetime != cfg.MaxLifetime || got.MaxConnIdleTime != cfg.MaxIdleTime ||
		got.MaxConns != int32(cfg.MaxConns) || got.MinConns != int32(cfg.MinConns) || got.HealthCheckPeriod != cfg.HealthCheckPeriod {
		t.Fatal("explicit pool settings did not override DSN values")
	}
}

func TestPoolConfigRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		dsn  string
	}{
		{"negative max", Config{MaxConns: -1}, testDSN},
		{"negative min", Config{MinConns: -1}, testDSN},
		{"min exceeds max", Config{MaxConns: 2, MinConns: 3}, testDSN},
		{"negative lifetime", Config{MaxLifetime: -time.Second}, testDSN},
		{"negative idle", Config{MaxIdleTime: -time.Second}, testDSN},
		{"negative health", Config{HealthCheckPeriod: -time.Second}, testDSN},
		{"negative connect timeout", Config{ConnectTimeout: -time.Second}, testDSN},
		{"invalid DSN health", Config{}, testDSN + "&pool_health_check_period=0"},
		{"invalid DSN minimum", Config{}, testDSN + "&pool_min_conns=-1"},
		{"DSN minimum exceeds override", Config{MaxConns: 2}, testDSN + "&pool_min_conns=3"},
		{"DSN idle exceeds max", Config{MaxConns: 2}, testDSN + "&pool_min_idle_conns=3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.cfg.poolConfig(tc.dsn); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}
		})
	}
	if uint64(^uint(0)>>1) > math.MaxInt32 {
		value := int64(math.MaxInt32) + 1
		tooLarge := int(value)
		if _, err := (Config{MaxConns: tooLarge}).poolConfig(testDSN); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("overflowing count error = %v, want invalid input", err)
		}
	}
}
