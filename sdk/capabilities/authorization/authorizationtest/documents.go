package authorizationtest

import (
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// DocumentCount is the documents fixture's generated resource count: alice
// views two of every three, so a 50-ID page cannot hold her set.
const DocumentCount = 120

// DocID names generated document i.
func DocID(i int) string { return fmt.Sprintf("doc-%03d", i) }

// Documents is examples/auth-cms's model with one rename every vendor needs:
// platform's relation admin shares its name with the permission admin, so the
// relation is administrator.
func Documents() Fixture {
	f := Fixture{Name: "documents"}
	var aliceIDs []string
	for i := range DocumentCount {
		if i%3 != 0 {
			f.Facts = append(f.Facts, fact("document", DocID(i), "viewer", "user", "alice"))
			aliceIDs = append(aliceIDs, DocID(i))
		}
	}
	f.Facts = append(f.Facts,
		fact("document", DocID(0), "viewer", "user", "bob"),
		fact("project", "demo", "owner", "user", "olivia"),
		fact("project", "demo", "member", "user", "mike"),
		fact("project", "demo", "auditor", "user", "audra"),
		fact("platform", "main", "administrator", "user", "root"),
	)
	f.Checks = []CheckCase{
		check("direct grant", user("alice"), "view", res("document", DocID(1)), true),
		check("other principal denied", user("bob"), "view", res("document", DocID(1)), false),
		check("second principal's grant", user("bob"), "view", res("document", DocID(0)), true),
		check("unknown resource id denied", user("alice"), "view", res("document", "nope"), false),
		check("relation answer", user("alice"), "viewer", res("document", DocID(1)), true),
		check("relation answer denied", user("bob"), "viewer", res("document", DocID(1)), false),
		check("owner manage_access", user("olivia"), "manage_access", res("project", "demo"), true),
		check("member view", user("mike"), "view", res("project", "demo"), true),
		check("member not manage_access", user("mike"), "manage_access", res("project", "demo"), false),
		check("auditor audit", user("audra"), "audit", res("project", "demo"), true),
		check("platform admin", user("root"), "admin", res("platform", "main"), true),
		check("platform non-admin", user("alice"), "admin", res("platform", "main"), false),
		checkErr("undeclared permission", user("alice"), "edit", res("document", DocID(1)), sdk.ErrInvalidInput),
		checkErr("undeclared resource type", user("alice"), "view", res("widget", "w1"), sdk.ErrInvalidInput),
		checkErr("hyphenated permission", user("alice"), "can-view", res("document", DocID(1)), sdk.ErrInvalidInput),
		checkErr("empty principal id", user(""), "view", res("document", DocID(1)), sdk.ErrInvalidInput),
		checkErr("empty permission", user("alice"), "", res("document", DocID(1)), sdk.ErrInvalidInput),
		checkErr("empty resource id", user("alice"), "view", res("document", ""), sdk.ErrInvalidInput),
		// Outside the portable grammar: a plain deny, or sdk.ErrInvalidInput
		// where the implementation's grammar (WithGrammar) rejects them.
		check("wildcard principal id", user("*"), "view", res("document", DocID(1)), false),
		check("wildcard resource id", user("alice"), "view", res("document", "*"), false),
		check("300-byte id", user("alice"), "view", res("document", strings.Repeat("a", 300)), false),
		check("object over 256 bytes", user("alice"), "view", res("document", strings.Repeat("a", 250)), false),
	}
	f.Lookups = []LookupCase{
		lookup("limit 50 over 120", user("alice"), "view", "document", 50, aliceIDs...),
		lookup("limit 0 is the default", user("alice"), "view", "document", 0, aliceIDs...),
		lookup("limit 7 small pages", user("alice"), "view", "document", 7, aliceIDs...),
		lookup("empty result", user("carol"), "view", "document", 10),
		lookup("single grant", user("bob"), "view", "document", 1, DocID(0)),
		lookup("project view", user("mike"), "view", "project", 10, "demo"),
		lookupErr("negative limit", user("alice"), "view", "document", -1, sdk.ErrInvalidInput),
		lookupErr("limit above the maximum", user("alice"), "view", "document", 1_000_000, sdk.ErrInvalidInput),
		lookupErr("undeclared permission", user("alice"), "edit", "document", 10, sdk.ErrInvalidInput),
		lookupErr("undeclared resource type", user("alice"), "view", "widget", 10, sdk.ErrInvalidInput),
		lookupErr("lookup is over permissions only", user("alice"), "viewer", "document", 10, sdk.ErrInvalidInput),
	}
	return f
}
