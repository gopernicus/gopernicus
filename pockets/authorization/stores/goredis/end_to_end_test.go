//go:build integration

package goredis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
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
		data, err := tursostore.CacheMigrationsFS.ReadFile(tursostore.CacheMigrationsDir + "/0002_iam_tuple_cache.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error { _, err := tx.Exec(ctx, string(data)); return err }); err != nil {
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
		for _, file := range []string{"0001_iam_cache_invalidation.sql", "0002_iam_tuple_cache.sql"} {
			data, err := pgxstore.CacheMigrationsFS.ReadFile(pgxstore.CacheMigrationsDir + "/" + file)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(data)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repos, err := pgxstore.Repositories(ctx, db, pgxstore.WithSchema(schema), pgxstore.WithTupleCache())
	if err != nil {
		t.Fatal(err)
	}
	return repos
}

func endToEndModel() relationships.Schema {
	return relationships.Schema{ResourceTypes: map[string]relationships.ResourceTypeDef{
		"group":    {Relations: map[string]relationships.RelationDef{"member": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}}}},
		"space":    {Relations: map[string]relationships.RelationDef{"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "group", Relation: "member"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Direct("viewer"))}},
		"document": {Relations: map[string]relationships.RelationDef{"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "space"}}}}, Permissions: map[string]relationships.PermissionRule{"view": relationships.AnyOf(relationships.Through("parent", "view"))}},
	}}
}

func endToEndSeed(t testing.TB, repos authorization.Repositories, count int) ([]model.CheckRequest, []string, relationships.CreateRelationship) {
	t.Helper()
	member := relationships.CreateRelationship{ResourceType: "group", ResourceID: "engineering", Relation: "member", SubjectType: "user", SubjectID: "alice"}
	tuples := []relationships.CreateRelationship{member, {ResourceType: "space", ResourceID: "main", Relation: "viewer", SubjectType: "group", SubjectID: "engineering", SubjectRelation: "member"}}
	requests, ids := make([]model.CheckRequest, count), make([]string, count)
	for i := range requests {
		ids[i] = fmt.Sprintf("d%04d", i)
		tuples = append(tuples, relationships.CreateRelationship{ResourceType: "document", ResourceID: ids[i], Relation: "parent", SubjectType: "space", SubjectID: "main"})
		requests[i] = model.CheckRequest{Principal: model.PrincipalRef{Type: "user", ID: "alice"}, Resource: model.Resource{Type: "document", ID: ids[i]}, Permission: "view"}
	}
	if err := repos.Relationships.CreateRelationships(t.Context(), tuples); err != nil {
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
			backend, err := NewTupleCache(client, "sql-redis", WithLimits(Limits{MaxMutationBytes: 16384}))
			if err != nil {
				t.Fatal(err)
			}
			components, err := authorization.New(repos, authorization.WithRelationshipModel(endToEndModel()), authorization.WithTupleCache(backend, tuplecache.Policy{MaxStaleness: time.Minute}))
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
			if err := repos.Relationships.DeleteRelationshipTarget(t.Context(), member.ResourceType, member.ResourceID, member.Relation, member.Subject()); err != nil {
				t.Fatal(err)
			}
			check(true) // The explicitly accepted delivery lag is observable.
			if err := components.TupleCache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			check(false)
			if err := repos.Relationships.CreateRelationships(t.Context(), []relationships.CreateRelationship{member}); err != nil {
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
			transient := make([]relationships.CreateRelationship, 100)
			for i := range transient {
				transient[i] = relationships.CreateRelationship{ResourceType: "space", ResourceID: fmt.Sprint("transient-", i), Relation: "viewer", SubjectType: "user", SubjectID: "bob"}
			}
			if err := repos.Relationships.CreateRelationships(t.Context(), transient); err != nil {
				t.Fatal(err)
			}
			for _, tuple := range transient {
				if err := repos.Relationships.DeleteResourceRelationships(t.Context(), tuple.ResourceType, tuple.ResourceID); err != nil {
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
			limited, err := NewTupleCache(client, "sql-redis", WithLimits(Limits{MaxReadBytes: 128, MaxMutationBytes: 16384}))
			if err != nil {
				t.Fatal(err)
			}
			bounded, err := authorization.New(repos, authorization.WithRelationshipModel(endToEndModel()), authorization.WithTupleCache(limited, tuplecache.Policy{MaxStaleness: time.Minute}))
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
					opts := []authorization.Option{authorization.WithRelationshipModel(endToEndModel())}
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
