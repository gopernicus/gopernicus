package decisions

import "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"

type PermissionReader = relationships.PermissionReader
type CheckReader = relationships.CheckReader
type CheckReadSource = relationships.CheckReadSource
type Reader = relationships.Reader
type ReadModel = relationships.ReadModel
type SubjectRule = relationships.SubjectRule
type RelationTarget = relationships.RelationTarget
type RelationSetReader = relationships.RelationSetReader
type CreateRelationship = relationships.CreateRelationship

var NewReadModel = relationships.NewReadModel
var ErrInvalidRelation = relationships.ErrInvalidRelation
var ErrInvalidSchema = relationships.ErrInvalidSchema
var ErrExpansionBudgetExceeded = relationships.ErrExpansionBudgetExceeded
