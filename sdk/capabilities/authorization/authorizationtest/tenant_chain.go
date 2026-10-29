package authorizationtest

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ChainLength is the node chain's length: deeper than every implementation's
// traversal limit, so its far end is sdk.ErrUnavailable, never a deny.
const ChainLength = 60

// NodeID names chain node i; node-000 holds the grant.
func NodeID(i int) string { return fmt.Sprintf("node-%03d", i) }

// TenantChain is a tenant/space/item hierarchy with a global role on
// platform:global and a recursive node chain for traversal depth.
func TenantChain() Fixture {
	f := Fixture{Name: "tenant-chain", Global: &GlobalResource{Type: "platform", ID: "global"}}
	f.Facts = []Fact{
		fact("tenant", "acme", "owner", "user", "tom"),
		fact("tenant", "acme", "staff_team", "user", "gina"),
		fact("space", "s1", "tenant", "tenant", "acme"),
		fact("space", "s1", "editor", "user", "ed"),
		fact("space", "s1", "viewer", "user", "vic"),
		fact("space", "s2", "tenant", "tenant", "acme"),
		fact("space", "s2", "parent", "space", "s1"),
		fact("space", "s2", "admin", "user", "sam"),
		fact("timeline", "t1", "space", "space", "s1"),
		fact("timeline", "t1", "vendor_collaborator", "user", "vera"),
		fact("timeline", "t2", "space", "space", "s2"),
		fact("timeline", "t2", "viewer", "user", "gus"),
		fact("dashboard", "d1", "space", "space", "s1"),
		fact("platform", "global", "platform_admin", "user", "pat"),
		fact("node", NodeID(0), "viewer", "user", "val"),
	}
	for i := 1; i < ChainLength; i++ {
		f.Facts = append(f.Facts, fact("node", NodeID(i), "parent", "node", NodeID(i-1)))
	}
	f.Checks = []CheckCase{
		check("timeline view through space", user("vic"), "view", res("timeline", "t1"), true),
		check("timeline manage denied to space viewer", user("vic"), "manage", res("timeline", "t1"), false),
		check("timeline edit through contribute", user("ed"), "edit", res("timeline", "t1"), true),
		check("vendor collaborator view", user("vera"), "view", res("timeline", "t1"), true),
		check("vendor collaborator not manage", user("vera"), "manage", res("timeline", "t1"), false),
		check("parent space confers nothing", user("vic"), "view", res("timeline", "t2"), false),
		check("space admin manages item", user("sam"), "manage", res("timeline", "t2"), true),
		check("guest direct item", user("gus"), "view", res("timeline", "t2"), true),
		check("guest no sibling", user("gus"), "view", res("timeline", "t1"), false),
		check("tool limits through tenant staff_team", user("gina"), "manage_tool_limits", res("space", "s1"), true),
		check("tool limits not tenant owner", user("tom"), "manage_tool_limits", res("space", "s1"), false),
		check("tenant owner manage", user("tom"), "manage", res("tenant", "acme"), true),
		check("platform admin has no implicit tenant", user("pat"), "manage", res("tenant", "acme"), false),
		check("global role relation", user("pat"), "platform_admin", res("platform", "global"), true),
		check("global role absent", user("tom"), "platform_admin", res("platform", "global"), false),
		check("dashboard view through space", user("ed"), "view", res("dashboard", "d1"), true),
		check("through depth 3", user("val"), "view", res("node", NodeID(3)), true),
		check("through depth 3 denied", user("vic"), "view", res("node", NodeID(3)), false),
		checkErr(fmt.Sprintf("%d-deep chain is unavailable", ChainLength-1), user("val"), "view", res("node", NodeID(ChainLength-1)), sdk.ErrUnavailable),
	}
	f.Lookups = []LookupCase{
		lookup("timeline view", user("vic"), "view", "timeline", 10, "t1"),
		lookup("timeline manage", user("sam"), "manage", "timeline", 10, "t2"),
		lookup("dashboard through space", user("ed"), "view", "dashboard", 10, "d1"),
	}
	return f
}
