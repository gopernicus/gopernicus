package relationships

import "testing"

func TestReadModelOwnsInputAndZeroDenies(t *testing.T) {
	rules := []SubjectRule{{ResourceType: "doc", Relation: "viewer", SubjectType: "user"}}
	model := NewReadModel(rules)
	encoded := model.JSON()
	rules[0].SubjectType = "service_account"
	if !model.Allows("doc", "viewer", "user", "") || model.Allows("doc", "viewer", "service_account", "") || model.JSON() != encoded {
		t.Fatal("caller mutation changed model")
	}
	if (ReadModel{}).Allows("doc", "viewer", "user", "") || (ReadModel{}).JSON() != "[]" {
		t.Fatal("zero model must deny all")
	}
	if NewReadModel([]SubjectRule{{ResourceType: "doc", Relation: "viewer", SubjectType: "user"}, {ResourceType: "doc", Relation: "viewer", SubjectType: "user"}}).JSON() != encoded {
		t.Fatal("duplicates changed model")
	}
}
