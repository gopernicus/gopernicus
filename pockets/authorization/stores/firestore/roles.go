package firestore

import (
	"context"
	"errors"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ role.Storer = (*roleStore)(nil)

// roleStore fills role.Storer over the iam_roles collection, whose document id
// IS the unique 5-tuple (SCHEMA.md §3.2). Every method refuses an ambient
// transaction first (R1), and every document it touches is addressed through
// grants.go — the file that owns the collection.
type roleStore struct {
	db *firestoredb.DB
}

func newRoleStore(db *firestoredb.DB) *roleStore {
	return &roleStore{db: db}
}

// Assign stores a role grant idempotently. The SQL siblings say
// `ON CONFLICT DO NOTHING` on the 5-tuple index; here the 5-tuple IS the
// document id, so the equivalent is one transaction that reads that document
// and writes only when it is absent — a duplicate is a no-op that RETAINS the
// existing row untouched, its original store-stamped created_at included.
//
// The check and the write are one transaction rather than a bare Create whose
// AlreadyExists is swallowed: ruling R3's "never check-then-write outside a
// transaction". The read is in the transaction's read set, so a concurrent
// first-writer aborts this commit and the vendor re-runs the callback, which
// then observes the winner and writes nothing. now is stamped INSIDE the
// callback because a retried attempt is a fresh attempt.
func (s *roleStore) Assign(ctx context.Context, a role.Assignment) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		exists, err := roleExists(ctx, s.db, s.db.ReaderFrom(ctx), a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		row := newRoleDoc(a)
		row.CreatedAt = time.Now().UTC()
		return putRole(ctx, s.db, s.db.WriterFrom(ctx), row)
	})
}

// Unassign removes an exact assignment. It is ONE delete on the deterministic
// document and carries NO existence precondition, so an absent assignment is
// nil rather than a port error — the idempotency the SQL siblings get from
// "zero rows deleted". A single document delete is atomic on its own, so no
// transaction is opened.
func (s *roleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return dropRole(ctx, s.db, s.db.WriterFrom(ctx), subjectType, subjectID, roleName, resourceType, resourceID)
}

// HasExactRole reports whether an assignment exists at the EXACT scope: one Get
// on the deterministic document, never a query. A global grant does not satisfy
// a scoped lookup and vice versa, because the scope pair is part of the id; the
// global-fallback rule is the service's (rolesvc.Service.HasRole, Q5), never
// this store's.
func (s *roleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return false, err
	}
	return roleExists(ctx, s.db, s.db.ReaderFrom(ctx), subjectType, subjectID, roleName, resourceType, resourceID)
}

// ListBySubject pages a subject's assignments in the contractual order
// (created_at DESC by default, role_key as the tiebreak). The subject is ONE
// equality clause on the derived subject_key, which hashes exactly the
// (subject_type, subject_id) pair the SQL siblings match with two columns.
func (s *roleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	base := rolesQuery(s.db).Where("subject_key", "==", roleSubjectKey(subjectType, subjectID))
	page, err := firestoredb.List(ctx, s.db.ReaderFrom(ctx), listRoles(base), req)
	if err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.MapPage(page, roleDoc.toAssignment), nil
}

// ListByResource is the RAW direct-scope listing: the assignments stored EXACTLY
// at (resourceType, resourceID), never the globally-granted subjects. The scope
// is ONE equality clause on the derived resource_key — and because the GLOBAL
// scope is the empty pair stored as empty strings (never null, never absent),
// listing ("", "") returns exactly the global grants, as the SQL siblings' two
// empty-string equality clauses do.
func (s *roleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	base := rolesQuery(s.db).Where("resource_key", "==", resourceKey(resourceType, resourceID))
	page, err := firestoredb.List(ctx, s.db.ReaderFrom(ctx), listRoles(base), req)
	if err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.MapPage(page, roleDoc.toAssignment), nil
}

// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the
// direct scoped assignments unioned with the global assignments a scoped
// HasRole falls back to, de-duplicated by (subject, role) and tagged with
// provenance. The algorithm is effective.go's stream merge — see
// listEffectiveByResource for why it is a dedicated reader rather than a filter
// over one query.
func (s *roleStore) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[role.EffectiveGrant]{}, err
	}
	return listEffectiveByResource(ctx, s.db, resourceType, resourceID, req)
}

// LookupResourceIDsBySubjectAndRoles is the roles kind's resource-id lookup.
//
// The UNRESTRICTED short-circuit runs FIRST and issues NO query: a global grant
// of a queried role is one deterministic document per role, so the probe is a
// single GetAll on the computed ids. A hit means the subject reaches every
// resource of the type and there is nothing to page — the port's (nil, true,
// nil).
//
// Otherwise it is the distinct resource-id keyset merge: the roles are chunked
// to the lookup budget and each chunk streams (resource_id, document name) in
// raw-byte order from resource_id > after, with duplicates folded BEFORE the
// limit applies (one resource granted by two queried roles is ONE id). An empty
// role set is (nil, false, nil) with no I/O at all: no role grants nothing.
//
// The probe and every chunk stream run under ONE snapshot, so a grant made
// between the probe and the scan cannot produce a page that is neither the
// before-state nor the after-state.
func (s *roleStore) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, false, err
	}
	if len(roles) == 0 {
		return nil, false, nil
	}

	var (
		out          []string
		unrestricted bool
	)
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		out, unrestricted = nil, false

		names := distinctSortedIDs(roles)
		global, err := anyGlobalRole(ctx, s.db, r, subjectType, subjectID, names)
		if err != nil {
			return err
		}
		if global {
			unrestricted = true
			return nil
		}

		var streams []*idStream
		for _, chunk := range chunkStrings(names, lookupChunkBudget) {
			q := whereAnyOf(rolesQuery(s.db).
				Where("subject_key", "==", roleSubjectKey(subjectType, subjectID)).
				Where("resource_type", "==", resourceType), "role", chunk)
			streams = append(streams, newIDStream(r, q, after, limit, roleResourceID))
		}
		out, err = mergeDistinctIDs(ctx, streams, limit)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return out, unrestricted, nil
}

// roleExists reports whether the exact grant is stored. Get returns the snapshot
// ALONGSIDE its sdk.ErrNotFound (the connector preserves the vendor's shape), so
// absence is a value here and not a second read.
func roleExists(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	snap, err := r.Get(ctx, roleRef(db, subjectType, subjectID, roleName, resourceType, resourceID))
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return false, err
	}
	return snap != nil && snap.Exists(), nil
}

// anyGlobalRole is the unrestricted probe: does the subject hold ANY of the
// queried roles at the GLOBAL scope? Every candidate is a deterministic document
// id, so this is one GetAll rather than a query — no index, no chunk arithmetic,
// and a missing document is a snapshot whose Exists() is false rather than an
// error. The role set is the caller's compiled granting roles (a handful), so
// the ref list is small by construction.
func anyGlobalRole(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, subjectType, subjectID string, roles []string) (bool, error) {
	refs := make([]*gcfs.DocumentRef, 0, len(roles))
	for _, name := range roles {
		refs = append(refs, roleRef(db, subjectType, subjectID, name, "", ""))
	}
	snaps, err := r.GetAll(ctx, refs)
	if err != nil {
		return false, err
	}
	for _, snap := range snaps {
		if snap != nil && snap.Exists() {
			return true, nil
		}
	}
	return false, nil
}
