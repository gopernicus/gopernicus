package memory

import (
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func (r *Relationships) ForModel(model relationships.ReadModel) relationships.Reader {
	return &Relationships{st: r.st, model: &model}
}

func (r *Relationships) allows(row relRow) bool {
	return r.model == nil || r.model.Allows(row.resourceType, row.relation, row.subjectType, row.subjectRelation)
}
