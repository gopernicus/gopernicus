//go:build integration

package goredis

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	pgxstore "github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx"
	tursostore "github.com/gopernicus/gopernicus/pockets/authorization/stores/turso"
	_ "modernc.org/sqlite"
)

var errEndToEndAck = errors.New("injected SQL acknowledgement failure")

type observedSource struct {
	tuplecache.Source
	reads   atomic.Uint64
	failAck atomic.Bool
}

func (s *observedSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	s.reads.Add(1)
	return s.Source.ReadSnapshot(ctx, fn)
}

func (s *observedSource) Acknowledge(ctx context.Context, before, after string, ids []string) error {
	if s.failAck.Load() {
		return errEndToEndAck
	}
	return s.Source.Acknowledge(ctx, before, after, ids)
}

func endToEndRepositories(t testing.TB, dialect string) authorization.Repositories {
	t.Helper()
	ctx := t.Context()
	if dialect == "sqlite" {
		db, err := tursodb.Open(ctx, tursodb.Config{URL: "file:" + filepath.Join(t.TempDir(), "authority.sqlite"), MaxOpenConns: 4, MaxIdleConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
		if err := tursodb.RunMigrations(ctx, db, tursostore.MigrationsFS, tursostore.MigrationsDir); err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error {
			return applyCacheBaseline(tursostore.TupleCacheMigrationsFS, tursostore.TupleCacheMigrationsDir, func(script string) error { _, err := tx.Exec(ctx, script); return err })
		}); err != nil {
			t.Fatal(err)
		}
		repos, err := tursostore.Repositories(ctx, db, tursostore.WithTupleCache())
		if err != nil {
			t.Fatal(err)
		}
		return repos
	}
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN unset — PostgreSQL to Redis integration NOT verified")
	}
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Each case owns a new schema; never truncate a caller's existing tables.
	name := "tuple_e2e_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	schema, err := pgxdb.NewSchema(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
			t.Errorf("drop isolated integration schema: %v", err)
		}
	})
	if err := pgxdb.RunMigrations(ctx, db, pgxstore.MigrationsFS, pgxstore.MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	if err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+name); err != nil {
			return err
		}
		return applyCacheBaseline(pgxstore.TupleCacheMigrationsFS, pgxstore.TupleCacheMigrationsDir, func(script string) error { _, err := tx.Exec(ctx, script); return err })
	}); err != nil {
		t.Fatal(err)
	}
	repos, err := pgxstore.Repositories(ctx, db, pgxstore.WithSchema(schema), pgxstore.WithTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	return repos
}

func endToEndModel() decisions.Model {
	return decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"group":    {Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}}},
		"space":    {Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "group", Relation: "member"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Any(decisions.Direct("viewer"))}},
		"document": {Relations: map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "space"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.Any(decisions.Through("parent", "view"))}},
	}}
}

func endToEndSeed(t testing.TB, repos authorization.Repositories, count int) ([]model.CheckRequest, []string, tuples.Tuple) {
	t.Helper()
	member := tuple("group", "engineering", "member", "user", "alice", "")
	facts := []tuples.Tuple{member, tuple("space", "main", "viewer", "group", "engineering", "member")}
	requests, ids := make([]model.CheckRequest, count), make([]string, count)
	for i := range requests {
		ids[i] = fmt.Sprintf("d%04d", i)
		facts = append(facts, tuple("document", ids[i], "parent", "space", "main", ""))
		requests[i] = model.CheckRequest{Principal: model.PrincipalRef{Type: "user", ID: "alice"}, Resource: model.Resource{Type: "document", ID: ids[i]}, Permission: "view"}
	}
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	return requests, ids, member
}

func TestSQLRedisDeliveryRecovery(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			repos := endToEndRepositories(t, dialect)
			requests, ids, member := endToEndSeed(t, repos, 128)
			source := &observedSource{Source: repos.TupleSource}
			repos.TupleSource = source
			client, _ := startRedis(t, "", false)
			backend, err := NewTupleCache(client, "sql-redis", WithLimits(Limits{MaxMutationBytes: 32768}))
			if err != nil {
				t.Fatal(err)
			}
			components, err := authorization.New(repos, authorization.WithModel(endToEndModel()), authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Minute}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = components.TupleCache.Close() })
			check := func(want bool) {
				t.Helper()
				results, err := components.Decisions.CheckBatch(t.Context(), requests)
				if err != nil || len(results) != len(requests) {
					t.Fatalf("batch: %d/%v", len(results), err)
				}
				for _, result := range results {
					if result.Allowed != want {
						t.Fatalf("wanted allowed=%v: %+v", want, result)
					}
				}
				filtered, err := components.Decisions.FilterAuthorized(t.Context(), requests[0].Principal, "view", "document", ids)
				if err != nil || (want && len(filtered) != len(ids)) || (!want && len(filtered) != 0) {
					t.Fatalf("filter: %d/%v", len(filtered), err)
				}
			}
			check(true)
			if source.reads.Load() != 2 {
				t.Fatal("cold checks did not use durable snapshots")
			}
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			check(true)
			if source.reads.Load() != 2 {
				t.Fatal("warm checks used SQL")
			}
			if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{member}}); err != nil {
				t.Fatal(err)
			}
			check(true) // The explicitly accepted delivery lag is observable.
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			check(false)
			if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{member}}); err != nil {
				t.Fatal(err)
			}
			source.failAck.Store(true)
			if err := components.TupleCache.Poll(t.Context()); !errors.Is(err, errEndToEndAck) {
				t.Fatalf("ack failure: %v", err)
			}
			check(true)
			source.failAck.Store(false)
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := client.Del(t.Context(), backend.key).Err(); err != nil {
				t.Fatal(err)
			}
			check(true)
			if source.reads.Load() != 4 {
				t.Fatal("lost mirror did not use durable snapshots")
			}
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Large obsolete backlog, small current graph. Delta cannot fit the
			// configured publication limit; explicit rebuild must still recover.
			transient := make([]tuples.Tuple, 100)
			for i := range transient {
				transient[i] = tuple("space", fmt.Sprint("transient-", i), "viewer", "user", "bob", "")
			}
			if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: transient}); err != nil {
				t.Fatal(err)
			}
			for _, fact := range transient {
				if err := repos.Tuples.DeleteScope(t.Context(), fact.Scope); err != nil {
					t.Fatal(err)
				}
			}
			before, err := backend.State(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := components.TupleCache.Poll(t.Context()); !errors.Is(err, tuplecache.ErrCapacity) {
				t.Fatalf("oversized delta: %v", err)
			}
			after, err := backend.State(t.Context())
			if err != nil || after != before {
				t.Fatalf("oversized delta changed mirror: %v/%v", after, err)
			}
			pending, err := source.Snapshot(t.Context(), before.Receipt)
			if err != nil || pending.Full || len(pending.Changes) != 2*len(transient) {
				t.Fatalf("oversized delta lost pending events: %+v/%v", pending, err)
			}
			if err := components.TupleCache.Rebuild(t.Context()); err != nil {
				t.Fatal(err)
			}
			check(true)
			state, err := backend.State(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := source.Snapshot(t.Context(), state.Receipt)
			if err != nil || snapshot.Full || len(snapshot.Changes) != 0 {
				t.Fatalf("recovery left work or inconsistent receipt: %+v/%v", snapshot, err)
			}
			limited, err := NewTupleCache(client, "sql-redis", WithLimits(Limits{MaxReadBytes: 128, MaxMutationBytes: 32768}))
			if err != nil {
				t.Fatal(err)
			}
			bounded, err := authorization.New(repos, authorization.WithModel(endToEndModel()), authorization.WithTupleCache(limited, tuplecache.Policy{MaxStaleness: time.Minute}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bounded.TupleCache.Close() })
			if err := bounded.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			results, err := bounded.Decisions.CheckBatch(t.Context(), requests)
			if err != nil || len(results) != len(requests) || bounded.TupleCache.Stats().CapacityFallbacks != 1 {
				t.Fatalf("bounded read did not fall back: %v/%+v", err, bounded.TupleCache.Stats())
			}
			for _, result := range results {
				if !result.Allowed {
					t.Fatal("capacity fallback lost grant")
				}
			}
		})
	}
}

func TestSQLRedisModelFreeRoleReads(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			repos := endToEndRepositories(t, dialect)
			principal := model.PrincipalRef{Type: "user", ID: "alice"}
			resource := model.Resource{Type: "space", ID: "a"}
			owner := tuple("space", "a", "owner", "user", "alice", "")
			member := owner
			member.Relation = "member"
			global := owner
			global.Scope, global.Relation = tuples.Global(), "admin"
			globalUserset := global
			globalUserset.Relation = "opaque-team-role"
			globalUserset.Subject = tuples.SubjectRef{Type: "group", ID: "engineering", Relation: "member"}
			if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{owner, member, global, globalUserset}}); err != nil {
				t.Fatal(err)
			}
			source := &observedSource{Source: repos.TupleSource}
			repos.TupleSource = source
			client, _ := startRedis(t, "", false)
			backend := newCache(t, client)
			components, err := authorization.New(repos, authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Minute}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = components.TupleCache.Close() })
			check := func(wantAdmin, wantOwner bool) {
				t.Helper()
				if held, err := components.Roles.HasRole(t.Context(), principal, "admin"); err != nil || held != wantAdmin {
					t.Fatalf("global exact role: %v/%v", held, err)
				}
				if held, err := components.Roles.HasRoleIn(t.Context(), principal, "owner", resource); err != nil || held != wantOwner {
					t.Fatalf("scoped exact owner: %v/%v", held, err)
				}
				if held, err := components.Roles.HasRoleIn(t.Context(), principal, "member", resource); err != nil || !held {
					t.Fatalf("independent member: %v/%v", held, err)
				}
				if held, err := components.Roles.HasRoleIn(t.Context(), principal, "admin", resource); err != nil || held {
					t.Fatalf("implicit global fallback: %v/%v", held, err)
				}
				if held, err := components.Roles.HasRole(t.Context(), principal, "opaque-team-role"); err != nil || held {
					t.Fatalf("userset expanded by exact role: %v/%v", held, err)
				}
			}
			check(true, true)
			if source.reads.Load() != 5 {
				t.Fatalf("cold roles used %d durable snapshots", source.reads.Load())
			}
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			check(true, true)
			if source.reads.Load() != 5 {
				t.Fatal("warm model-free roles used SQL")
			}
			if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{owner, global}}); err != nil {
				t.Fatal(err)
			}
			check(true, true) // Explicitly accepted delivery lag applies to roles too.
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			check(false, false)
			if source.reads.Load() != 5 {
				t.Fatal("delivered revocation did not use the shared mirror")
			}
		})
	}
}

func TestSQLRedisEventOrderPreservesFinalRevocation(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			repos := endToEndRepositories(t, dialect)
			fact := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
			if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{fact}}); err != nil {
				t.Fatal(err)
			}
			client, _ := startRedis(t, "", false)
			components, err := authorization.New(repos, authorization.WithTupleCache(newCache(t, client), tuplecache.Policy{MaxStaleness: time.Minute}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = components.TupleCache.Close() })
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			principal := model.PrincipalRef{Type: "user", ID: "alice"}
			if held, err := components.Roles.HasRole(t.Context(), principal, "admin"); err != nil || !held {
				t.Fatalf("bootstrap role: %v/%v", held, err)
			}
			// IDs 2..22 alternate revoke/grant, ending revoked. Lexical replay
			// ends at ID 9 (a grant), incorrectly resurrecting authority.
			for i := range 21 {
				change := tuples.Changes{Remove: []tuples.Tuple{fact}}
				if i%2 != 0 {
					change = tuples.Changes{Add: []tuples.Tuple{fact}}
				}
				if err := repos.Tuples.ApplyTuples(t.Context(), change); err != nil {
					t.Fatal(err)
				}
			}
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			if held, err := components.Roles.HasRole(t.Context(), principal, "admin"); err != nil || held {
				t.Fatalf("numeric event replay resurrected revoked role: %v/%v", held, err)
			}
			if stats := components.TupleCache.Stats(); stats.Hits != 2 || stats.Fallbacks != 0 {
				t.Fatalf("revocation must be proved in warm mirror: %+v", stats)
			}
		})
	}
}

func BenchmarkSQLRedisDecisions(b *testing.B) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, posture := range []string{"sql", "cold", "warm"} {
			for _, operation := range []string{"check", "batch128", "filter128"} {
				b.Run(dialect+"/"+posture+"/"+operation, func(b *testing.B) {
					repos := endToEndRepositories(b, dialect)
					requests, ids, _ := endToEndSeed(b, repos, 128)
					client, _ := startRedis(b, "", false)
					backend, err := NewTupleCache(client, "benchmark")
					if err != nil {
						b.Fatal(err)
					}
					opts := []authorization.Option{authorization.WithModel(endToEndModel())}
					if posture != "sql" {
						opts = append(opts, authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Hour}))
					}
					components, err := authorization.New(repos, opts...)
					if err != nil {
						b.Fatal(err)
					}
					if components.TupleCache != nil {
						b.Cleanup(func() { _ = components.TupleCache.Close() })
						if posture == "warm" {
							if err := components.TupleCache.Poll(b.Context()); err != nil {
								b.Fatal(err)
							}
						}
					}
					b.ReportAllocs()
					for b.Loop() {
						switch operation {
						case "check":
							result, err := components.Decisions.Check(b.Context(), requests[0])
							if err != nil || !result.Allowed {
								b.Fatalf("check: %+v/%v", result, err)
							}
						case "batch128":
							results, err := components.Decisions.CheckBatch(b.Context(), requests)
							if err != nil || len(results) != len(requests) {
								b.Fatalf("batch: %d/%v", len(results), err)
							}
							for _, result := range results {
								if !result.Allowed {
									b.Fatal("batch lost grant")
								}
							}
						case "filter128":
							results, err := components.Decisions.FilterAuthorized(b.Context(), requests[0].Principal, "view", "document", ids)
							if err != nil || len(results) != len(ids) {
								b.Fatalf("filter: %d/%v", len(results), err)
							}
						}
					}
					if cache := components.TupleCache; cache != nil {
						stats := cache.Stats()
						b.ReportMetric(float64(stats.Hits)/float64(b.N), "cache_hits/op")
						b.ReportMetric(float64(stats.Fallbacks)/float64(b.N), "fallbacks/op")
					}
				})
			}
		}
	}
}

func BenchmarkSQLRedisRoleReads(b *testing.B) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, size := range []int{1, 16, 128} {
			for _, posture := range []string{"sql", "cold", "warm"} {
				b.Run(fmt.Sprintf("%s/roles=%d/%s", dialect, size, posture), func(b *testing.B) {
					repos := endToEndRepositories(b, dialect)
					facts, leaves := make([]tuples.Tuple, size), make([]decisions.Expression, size)
					principal := model.PrincipalRef{Type: "user", ID: "alice"}
					resource := model.Resource{Type: "document", ID: "one"}
					for i := range facts {
						label := fmt.Sprintf("role-%03d", i)
						facts[i] = tuples.Tuple{Scope: tuples.Global(), Relation: label, Subject: tuples.SubjectRef{Type: principal.Type, ID: principal.ID}}
						leaves[i] = decisions.Role(label)
						if i%2 != 0 {
							facts[i].Scope = tuples.On(resource.Type, resource.ID)
							leaves[i] = decisions.RoleIn(label, resource)
						}
					}
					if err := repos.Tuples.ApplyTuples(b.Context(), tuples.Changes{Add: facts}); err != nil {
						b.Fatal(err)
					}
					var opts []authorization.Option
					if posture != "sql" {
						client, _ := startRedis(b, "", false)
						opts = append(opts, authorization.WithTupleCache(newCache(b, client), tuplecache.Policy{MaxStaleness: time.Hour}))
					}
					components, err := authorization.New(repos, opts...)
					if err != nil {
						b.Fatal(err)
					}
					if components.TupleCache != nil {
						b.Cleanup(func() { _ = components.TupleCache.Close() })
						if posture == "warm" {
							if err := components.TupleCache.Poll(b.Context()); err != nil {
								b.Fatal(err)
							}
						}
					}
					expression := decisions.All(leaves...)
					b.ReportAllocs()
					for b.Loop() {
						result, err := components.Decisions.Evaluate(b.Context(), principal, expression)
						if err != nil || !result.Allowed {
							b.Fatalf("model-free exact conjunction: %+v/%v", result, err)
						}
					}
					if cache := components.TupleCache; cache != nil {
						stats := cache.Stats()
						b.ReportMetric(float64(stats.Hits)/float64(b.N), "cache_hits/op")
						b.ReportMetric(float64(stats.Fallbacks)/float64(b.N), "fallbacks/op")
					}
				})
			}
		}
	}
}

// Fresh canonical caches have a separate baseline; legacy cache migrations
// reference tables removed by the canonical base migration.
func applyCacheBaseline(source fs.FS, dir string, apply func(string) error) error {
	entries, err := fs.ReadDir(source, dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(source, dir+"/"+entry.Name())
		if err != nil {
			return err
		}
		if err := apply(string(data)); err != nil {
			return err
		}
	}
	return nil
}
