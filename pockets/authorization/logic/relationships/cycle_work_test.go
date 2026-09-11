package relationships

import (
	"fmt"
	"strings"
	"testing"
)

func TestThroughCycleWalkExpandsConvergentSuffixOnce(t *testing.T) {
	// Every vertex has four paths into the next layer, so path-by-path DFS
	// grows exponentially even though the compiled dependency graph is small.
	const layers, width = 64, 4
	schema := Schema{ResourceTypes: make(map[string]ResourceTypeDef)}
	for layer := 0; layer < layers; layer++ {
		for branch := 0; branch < width; branch++ {
			name := fmt.Sprintf("node%02d_%d", layer, branch)
			def := ResourceTypeDef{Relations: map[string]RelationDef{"reader": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}}}, Permissions: make(map[string]PermissionRule)}
			checks := []PermissionCheck{Direct("reader")}
			if layer+1 < layers {
				for next := 0; next < width; next++ {
					relation := fmt.Sprintf("parent%d", next)
					def.Relations[relation] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: fmt.Sprintf("node%02d_%d", layer+1, next)}}}
					checks = append(checks, Through(relation, "view"))
				}
			}
			def.Permissions["view"] = AnyOf(checks...)
			schema.ResourceTypes[name] = def
		}
	}
	if _, err := Compile(schema); err != nil {
		t.Fatalf("convergent acyclic model: %v", err)
	}
	nodes, adjacency := throughGraph(schema)
	expanded := make(map[permissionKey]int)
	edges := 0
	cycle := findThroughCycle(nodes, func(key permissionKey) []permissionKey {
		expanded[key]++
		edges += len(adjacency[key])
		return adjacency[key]
	})
	if cycle != nil || len(expanded) != layers*width || edges != (layers-1)*width*width {
		t.Fatalf("cycle=%v expanded=%d edges=%d", cycle, len(expanded), edges)
	}
	for key, count := range expanded {
		if count != 1 {
			t.Fatalf("%v expanded %d times", key, count)
		}
	}
	// Closing one path must now fail, with a stable witness despite map order.
	leaf := schema.ResourceTypes["node63_0"]
	leaf.Relations["back"] = RelationDef{AllowedSubjects: []SubjectTypeRef{{Type: "node00_0"}}}
	leaf.Permissions["view"] = AnyOf(Direct("reader"), Through("back", "view"))
	var first string
	for i := 0; i < 10; i++ {
		_, err := Compile(schema)
		if err == nil || !strings.Contains(err.Error(), "circular through-relation") {
			t.Fatalf("cycle admitted: %v", err)
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("unstable cycle witness: %v", err)
		}
	}
}
