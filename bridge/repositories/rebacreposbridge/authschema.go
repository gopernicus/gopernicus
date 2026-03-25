// Package rebacreposbridge authorization schema customization.
// This file is created once and can be customized.

package rebacreposbridge

import (
	"github.com/gopernicus/gopernicus/core/auth/authorization"
)

// AuthSchema returns the authorization schema for the rebacreposbridge domain.
// The generated schema is returned by default.
//
// To customize, modify the returned schema:
//
//	func AuthSchema() []authorization.ResourceSchema {
//		schemas := GeneratedAuthSchema()
//		for i, s := range schemas {
//			if s.Name == "example" {
//				s.Def.Relations["custom_role"] = authorization.RelationDef{
//					AllowedSubjects: []authorization.SubjectTypeRef{
//						{Type: "user"},
//					},
//				}
//				s.Def.Permissions["custom_action"] = authorization.AnyOf(
//					authorization.Direct("custom_role"),
//				)
//				schemas[i] = s
//			}
//		}
//		return schemas
//	}
func AuthSchema() []authorization.ResourceSchema {
	return GeneratedAuthSchema()
}
