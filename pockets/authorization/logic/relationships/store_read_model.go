package relationships

import (
	"reflect"
)

func (s *CompiledSchema) readModel() ReadModel {
	var rules []SubjectRule
	for resourceType, resource := range s.resourceTypes {
		for relation, definition := range resource.relations {
			for _, subject := range definition.subjects {
				rules = append(rules, SubjectRule{ResourceType: resourceType, Relation: relation, SubjectType: subject.Type, SubjectRelation: subject.Relation})
			}
		}
	}
	return NewReadModel(rules)
}

func isNilReader(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
