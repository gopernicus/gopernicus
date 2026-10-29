package authorizationtest

import (
	"strings"

	"github.com/gopernicus/gopernicus/sdk/capabilities/authorization"
)

// Implementation names used as Run's implementation argument and as Diverge keys.
const (
	Engine  = "engine"
	Authzed = "authzed"
	OpenFGA = "openfga"
)

// Subject is a fact's subject. Relation is set only for a userset subject.
type Subject struct {
	Type     string
	ID       string
	Relation string
}

// Fact is one relationship in protocol terms. A fact on the fixture's
// GlobalResource is a global role; an implementation without such a concept
// stores it as an ordinary relation on that resource.
type Fact struct {
	Resource authorization.Resource
	Relation string
	Subject  Subject
}

// Expect is an expected outcome: Err (an sdk class sentinel, matched with
// errors.Is) or an answer.
type Expect struct {
	Allowed bool
	// IDs is compared as a set against the deduped union of every page.
	IDs []string
	Err error
}

// CheckCase is one Check with its expected answer. Diverge records a
// documented divergence for a named implementation.
type CheckCase struct {
	Name    string
	Req     authorization.CheckRequest
	Want    Expect
	Diverge map[string]Expect
}

// LookupCase is one lookup, drained page by page.
type LookupCase struct {
	Name    string
	Req     authorization.LookupRequest
	Want    Expect
	Diverge map[string]Expect
}

// GlobalResource is where a fixture's global roles live: a global role label
// is the relation RelationFor(label) on Type:ID.
type GlobalResource struct {
	Type           string
	ID             string
	RelationPrefix string
}

// Fixture is facts plus expected answers. The factory given to Run builds a
// fresh authority for it, installs its own dialect's schema for Name and seeds
// Facts natively.
type Fixture struct {
	Name    string
	Global  *GlobalResource
	Facts   []Fact
	Checks  []CheckCase
	Lookups []LookupCase
}

// Expect returns the outcome implementation must produce.
func (c CheckCase) Expect(implementation string) Expect {
	return expectFor(implementation, c.Want, c.Diverge)
}

// Expect returns the outcome implementation must produce.
func (c LookupCase) Expect(implementation string) Expect {
	return expectFor(implementation, c.Want, c.Diverge)
}

// RelationFor returns the relation that holds the global role label.
func (g GlobalResource) RelationFor(label string) string { return g.RelationPrefix + label }

// Label returns the global role label a relation on the global resource
// holds, or false when the relation is not a role relation.
func (g GlobalResource) Label(relation string) (string, bool) {
	if !strings.HasPrefix(relation, g.RelationPrefix) || len(relation) == len(g.RelationPrefix) {
		return "", false
	}
	return strings.TrimPrefix(relation, g.RelationPrefix), true
}

// Holds reports whether r is the global resource.
func (g GlobalResource) Holds(r authorization.Resource) bool { return r.Type == g.Type && r.ID == g.ID }

// Fixtures returns every fixture, freshly built.
func Fixtures() []Fixture {
	return []Fixture{Documents(), GlobalRoles(), TenantChain(), Usersets()}
}

// FixtureNamed returns one fixture by name.
func FixtureNamed(name string) (Fixture, bool) {
	for _, f := range Fixtures() {
		if f.Name == name {
			return f, true
		}
	}
	return Fixture{}, false
}

func expectFor(implementation string, want Expect, diverge map[string]Expect) Expect {
	if d, ok := diverge[implementation]; ok {
		return d
	}
	return want
}

func user(id string) authorization.Principal { return authorization.Principal{Type: "user", ID: id} }

func res(resourceType, id string) authorization.Resource {
	return authorization.Resource{Type: resourceType, ID: id}
}

func fact(resourceType, resourceID, relation, subjectType, subjectID string) Fact {
	return Fact{Resource: res(resourceType, resourceID), Relation: relation, Subject: Subject{Type: subjectType, ID: subjectID}}
}

func userset(resourceType, resourceID, relation, subjectType, subjectID, subjectRelation string) Fact {
	return Fact{Resource: res(resourceType, resourceID), Relation: relation, Subject: Subject{Type: subjectType, ID: subjectID, Relation: subjectRelation}}
}

func checkReq(p authorization.Principal, permission string, r authorization.Resource) authorization.CheckRequest {
	return authorization.CheckRequest{Principal: p, Permission: permission, Resource: r}
}

func check(name string, p authorization.Principal, permission string, r authorization.Resource, allowed bool) CheckCase {
	return CheckCase{Name: name, Req: checkReq(p, permission, r), Want: Expect{Allowed: allowed}}
}

func checkErr(name string, p authorization.Principal, permission string, r authorization.Resource, err error) CheckCase {
	return CheckCase{Name: name, Req: checkReq(p, permission, r), Want: Expect{Err: err}}
}

func lookupReq(p authorization.Principal, permission, resourceType string, limit int) authorization.LookupRequest {
	return authorization.LookupRequest{Principal: p, Permission: permission, ResourceType: resourceType, Limit: limit}
}

func lookup(name string, p authorization.Principal, permission, resourceType string, limit int, ids ...string) LookupCase {
	return LookupCase{Name: name, Req: lookupReq(p, permission, resourceType, limit), Want: Expect{IDs: append([]string{}, ids...)}}
}

func lookupErr(name string, p authorization.Principal, permission, resourceType string, limit int, err error) LookupCase {
	return LookupCase{Name: name, Req: lookupReq(p, permission, resourceType, limit), Want: Expect{Err: err}}
}
