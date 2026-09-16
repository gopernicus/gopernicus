package roles

import "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

// Assignment is an exact concrete-subject membership. Scope is always explicit.
type Assignment struct {
	SubjectType string
	SubjectID   string
	Role        string
	Scope       tuples.Scope
}

func (a Assignment) Validate() error { return a.Tuple().Validate() }
func (a Assignment) Tuple() tuples.Tuple {
	return tuples.Tuple{Scope: a.Scope, Relation: a.Role, Subject: tuples.SubjectRef{Type: a.SubjectType, ID: a.SubjectID}}
}
func assignment(t tuples.Tuple) Assignment {
	return Assignment{SubjectType: t.Subject.Type, SubjectID: t.Subject.ID, Role: t.Relation, Scope: t.Scope}
}
