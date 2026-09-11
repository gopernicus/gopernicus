package roles

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// Assignment is a stored role grant. The empty (ResourceType, ResourceID) pair
// is a GLOBAL assignment; a non-empty pair scopes it to that resource.
type Assignment struct {
	SubjectType  string
	SubjectID    string
	Role         string
	ResourceType string // "" with ResourceID "" ⇒ global
	ResourceID   string
}

// Validate checks the five opaque reference fields without consulting a role
// model. References use the same bounded, control-free UTF-8 grammar as
// relationship tuples. The resource pair may be empty for a global grant;
// otherwise both fields must be present. Values are never normalized.
func (a Assignment) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"subject type", a.SubjectType},
		{"subject id", a.SubjectID},
		{"role", a.Role},
	} {
		if err := authmodel.ValidateRefField(field.name, field.value); err != nil {
			return err
		}
	}
	if (a.ResourceType == "") != (a.ResourceID == "") {
		return fmt.Errorf("role resource scope requires both type and id: %w", sdk.ErrInvalidInput)
	}
	if a.ResourceType != "" {
		if err := authmodel.ValidateRefField("resource type", a.ResourceType); err != nil {
			return err
		}
		return authmodel.ValidateRefField("resource id", a.ResourceID)
	}
	return nil
}

// EffectiveGrant is one de-duplicated EFFECTIVE role grant on a resource,
// produced by [Storer.ListEffectiveByResource]. (SubjectType, SubjectID, Role)
// identify the grant; Direct and Global are its provenance:
//
//   - Direct: a scoped assignment is stored EXACTLY at the requested resource.
//   - Global: a global ("","") assignment exists that a scoped HasRole
//     satisfies as a fallback.
//
// Both may be true — the same subject holds the role directly AND globally. At
// least one is always true. A global grant is never rewritten as a scoped row:
// this type deliberately carries only the subject+role identity plus provenance,
// not a fabricated resource scope. Enumeration by Effective listing therefore
// describes the SAME grant set HasRole decides, without claiming the global
// assignment lives at the resource.
type EffectiveGrant struct {
	SubjectType string
	SubjectID   string
	Role        string
	Direct      bool
	Global      bool
}

// Provenance labels — WHERE a role grant was found. They are the kind's ONE
// provenance vocabulary: [EffectiveGrant.Provenance] returns them, and the
// service's provenance-reporting probe reports ProvenanceDirect/ProvenanceGlobal.
const (
	// ProvenanceDirect — an assignment stored EXACTLY at the requested scope.
	ProvenanceDirect = "direct"
	// ProvenanceGlobal — a global ("","") assignment satisfying a scoped query.
	ProvenanceGlobal = "global"
	// ProvenanceBoth — the same grant is held directly AND globally. It is an
	// enumeration-only label: a decision probe reports the more specific
	// ProvenanceDirect.
	ProvenanceBoth = "both"
)

// Provenance returns the grant's provenance label — ProvenanceDirect,
// ProvenanceGlobal, or ProvenanceBoth. A grant always has at least one source,
// so the zero label never occurs on a value returned by the store.
func (g EffectiveGrant) Provenance() string {
	switch {
	case g.Direct && g.Global:
		return ProvenanceBoth
	case g.Global:
		return ProvenanceGlobal
	default:
		return ProvenanceDirect
	}
}

// Storer is the storage contract for the roles kind — plain lookups, no graph
// walk. The listing methods are list-typed with the same cursor/tiebreak
// conventions as the relationship listings.
//
// Ambient transactions. When ctx carries the connector's Transact-owned
// transaction (sdk/capabilities/transaction.Transactor), every method of the store runs
// ON that transaction and never opens, commits, or rolls back one of its own;
// the enclosing Transact decides the outcome from its callback's return value,
// so a host must return a write error from that callback to roll the workflow
// back. Outside an ambient transaction behavior is unchanged. Same contract as
// relationship.Storer, so a role assignment and the relationship tuples written
// beside it in one Transact commit or roll back together.
type Storer interface {
	// Assign inserts a role assignment. It is idempotent: a duplicate (same
	// subject, role, and resource scope) is a no-op returning nil. Implementations
	// validate the assignment before writing, including direct store calls.
	Assign(ctx context.Context, a Assignment) error

	// Unassign removes an exact assignment. It is idempotent: removing an absent
	// assignment (zero rows) returns nil.
	Unassign(ctx context.Context, subjectType, subjectID, role, resourceType, resourceID string) error

	// HasExactRole reports whether an assignment exists at the EXACT scope. A
	// global assignment does not satisfy a scoped lookup and vice versa — the
	// global-fallback rule lives in the service (roles.Service.HasRole, Q5),
	// not here.
	HasExactRole(ctx context.Context, subjectType, subjectID, role, resourceType, resourceID string) (bool, error)

	// ListBySubject pages a subject's assignments by role_key ASC: the validated
	// subject type/id, role, resource type/id joined with U+0001 in byte order.
	ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[Assignment], error)

	// ListByResource is the RAW direct-scope listing: it pages the assignments
	// stored EXACTLY at (resourceType, resourceID) and never surfaces
	// globally-granted subjects. It is not effective — use it to inspect what is
	// stored at a scope, never to enumerate effective access.
	ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[Assignment], error)

	// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the
	// union of the direct scoped assignments at (resourceType, resourceID) with
	// the global assignments a scoped HasRole satisfies, de-duplicated by
	// (subject, role) and each tagged with its provenance. Rows are ordered
	// deterministically by the (subject_type, subject_id, role) grant key
	// ascending BEFORE pagination, so the keyset cursor is stable. A global grant
	// is not rewritten as a scoped row (see [EffectiveGrant]). When the requested
	// scope is itself global ("",""), there is no fallback and every grant is
	// Direct — matching HasRole's no-fallback path for an unscoped query.
	ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[EffectiveGrant], error)

	// LookupResourceIDsBySubjectAndRoles is the roles kind's resource-id lookup
	// (authorization-lookup-paging, A3b). When the subject holds ANY of roles
	// GLOBALLY ("", "") it reports unrestricted=true with nil ids — the subject
	// reaches every resource of the type and the caller must skip id filtering.
	// Otherwise it returns the DISTINCT resource_id values of resourceType at
	// which the subject holds any of roles: sorted ascending in byte order,
	// strictly greater than after (after == "" means from the start), at most
	// limit of them (a non-positive limit means unbounded). An empty roles is
	// (nil, false, nil). It applies no model knowledge: the caller passes the
	// compiled granting roles.
	LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) (ids []string, unrestricted bool, err error)
}
