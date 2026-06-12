package generators

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// The TypeScript client is generated straight from bridge data — the same
// source of truth as the handlers — so its types are lossless (enum literal
// unions, nullability, required-ness) where the runtime OpenAPI JSON is not.
// Emitted only when gopernicus.yml carries a clients.typescript section.

// TSClientEntity is one bridged entity's contribution to the client: the
// entity interface, request types, and one namespace of route methods.
type TSClientEntity struct {
	EntityName string // PascalCase singular, e.g. "Tenant"
	Namespace  string // client property, lowercase plural, e.g. "tenants"

	Fields       []TSField // full entity row (response shape)
	CreateFields []TSField // create request body ("" when no create route)
	UpdateFields []TSField // update request body

	Routes []TSRoute
}

// TSField is one property of a generated interface.
type TSField struct {
	Name     string // JSON name (snake_case column)
	Type     string // rendered TS type, e.g. `string | null`, `"a" | "b"`
	Optional bool   // request fields the API does not require
}

// TSRoute is one client method.
type TSRoute struct {
	MethodName string   // camelCase, e.g. "list", "get", "softDelete"
	HTTPMethod string   // GET/POST/PUT/DELETE
	PathExpr   string   // TS template literal, e.g. `/tenants/${p(tenantId)}`
	PathParams []string // camelCase arg names in path order
	HasBody    bool
	BodyType   string // e.g. "CreateTenantRequest"
	// ResponseKind: "page" → PageResponse<Entity>, "record" →
	// RecordResponse<Entity>, "none" → void (204).
	ResponseKind  string
	Authenticated bool
	// List options (ResponseKind page): filter params + search.
	FilterFields []TSField
	HasSearch    bool
}

// BuildTSClientEntity projects one generated bridge into client data.
func BuildTSClientEntity(data *BridgeTemplateData, resolved *ResolvedFile) TSClientEntity {
	e := TSClientEntity{
		EntityName: data.EntityName,
		Namespace:  strings.ToLower(resolved.EntityPlural),
	}

	for _, col := range resolved.AllColumns {
		e.Fields = append(e.Fields, TSField{
			Name: col.Name,
			Type: tsTypeForColumn(col.GoType, col.IsEnum, col.EnumValues),
		})
	}
	if len(data.CreateQueries) > 0 {
		e.CreateFields = tsRequestFields(data.CreateQueries[0].Fields)
	}
	if len(data.UpdateQueries) > 0 {
		e.UpdateFields = tsRequestFields(data.UpdateQueries[0].Fields)
	}

	for _, r := range data.Routes {
		route := TSRoute{
			MethodName:    tsMethodName(r.FuncName),
			HTTPMethod:    r.Method,
			Authenticated: r.Authenticated != "",
		}
		route.PathExpr, route.PathParams = tsPathExpr(r.Path)

		switch r.Category {
		case "list":
			route.ResponseKind = "page"
			for _, lq := range data.ListQueries {
				if lq.FuncName != r.FuncName {
					continue
				}
				route.FilterFields = tsRequestFields(lq.FilterFields)
				route.HasSearch = lq.HasSearch
				break
			}
		case "create", "update", "scan_one":
			route.ResponseKind = "record"
		default: // exec (delete, lifecycle transitions) → 204
			route.ResponseKind = "none"
		}
		switch r.FuncName {
		case "Create":
			route.HasBody = true
			route.BodyType = "Create" + data.EntityName + "Request"
		case "Update":
			route.HasBody = true
			route.BodyType = "Update" + data.EntityName + "Request"
		}
		e.Routes = append(e.Routes, route)
	}
	return e
}

// emitTypeScriptClient writes the client package when the manifest enables
// it. Entities are sorted for deterministic output.
func emitTypeScriptClient(cfg Config, entities []TSClientEntity, opts Options) error {
	dir := cfg.Manifest.TypeScriptClientDir()
	if dir == "" {
		return nil
	}
	outDir := filepath.Join(cfg.ProjectRoot, dir)

	sort.Slice(entities, func(i, j int) bool { return entities[i].Namespace < entities[j].Namespace })

	fmt.Printf("\n  %s/ (TypeScript client)\n", dir)

	data := tsClientTemplateData{Entities: entities}
	files := []struct {
		name      string
		tmpl      string
		bootstrap bool
	}{
		{"envelopes.gen.ts", tsEnvelopesTemplate, false},
		{"types.gen.ts", tsTypesTemplate, false},
		{"client.gen.ts", tsClientTemplate, false},
		{"index.ts", tsIndexTemplate, false},
		{"client.ts", tsClientBootstrapTemplate, true},
		// tsconfig is created once but carries no marker — JSON has no
		// comment syntax to host one.
		{"tsconfig.json", tsConfigTemplate, true},
	}
	for _, f := range files {
		path := filepath.Join(outDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}
		out, err := renderTSTemplate(f.name, f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", f.name, err)
		}
		if f.bootstrap && f.name == "client.ts" {
			out = prependBootstrapMarker("tsclient/client.ts", out)
		}
		if err := writeFile(path, out, opts); err != nil {
			return err
		}
		verb := "write"
		if f.bootstrap {
			verb = "create"
		}
		fmt.Printf("      %s %s\n", verb, path)
	}
	return nil
}

type tsClientTemplateData struct {
	Entities []TSClientEntity
}

func renderTSTemplate(name, tmplText string, data tsClientTemplateData) ([]byte, error) {
	tmpl, err := template.New(name).Funcs(template.FuncMap{
		"lcFirst": func(s string) string {
			if s == "" {
				return s
			}
			return strings.ToLower(s[:1]) + s[1:]
		},
	}).Parse(tmplText)
	if err != nil {
		return nil, err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// tsRequestFields maps bridge request fields to TS request properties.
func tsRequestFields(fields []BridgeField) []TSField {
	var out []TSField
	for _, f := range fields {
		out = append(out, TSField{
			Name:     f.DBName,
			Type:     tsTypeForColumn(f.GoType, f.IsEnum, f.EnumValues),
			Optional: !f.IsRequired,
		})
	}
	return out
}

// tsTypeForColumn maps a Go field type to its TS rendering. Time values
// travel as ISO strings; json columns are unknown (the consumer narrows).
func tsTypeForColumn(goType string, isEnum bool, enumValues []string) string {
	nullable := strings.HasPrefix(goType, "*")
	inner := strings.TrimPrefix(goType, "*")

	var ts string
	switch {
	case isEnum && len(enumValues) > 0:
		quoted := make([]string, len(enumValues))
		for i, v := range enumValues {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		ts = strings.Join(quoted, " | ")
	case inner == "string", inner == "time.Time", inner == "[]byte":
		ts = "string"
	case inner == "bool":
		ts = "boolean"
	case strings.HasPrefix(inner, "int"), strings.HasPrefix(inner, "uint"), strings.HasPrefix(inner, "float"):
		ts = "number"
	case inner == "json.RawMessage":
		ts = "unknown"
	default:
		ts = "unknown"
	}
	if nullable {
		ts += " | null"
	}
	return ts
}

// tsMethodName camel-cases a route func name: List → list, SoftDelete →
// softDelete, GetByEmail → getByEmail.
func tsMethodName(funcName string) string {
	if funcName == "" {
		return funcName
	}
	return strings.ToLower(funcName[:1]) + funcName[1:]
}

// tsPathExpr converts "/tenants/{tenant_id}" into a TS template literal
// over camelCase args: `/tenants/${p(tenantId)}` plus the arg list.
func tsPathExpr(path string) (string, []string) {
	var params []string
	var b strings.Builder
	rest := path
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			b.WriteString(rest)
			break
		}
		arg := ToCamelCase(rest[open+1 : open+closing])
		params = append(params, arg)
		b.WriteString(rest[:open])
		b.WriteString("${p(" + arg + ")}")
		rest = rest[open+closing+1:]
	}
	return "`" + b.String() + "`", params
}
