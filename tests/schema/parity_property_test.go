// Feature: tenant-environment, Property 2: schema.yaml and definition.yaml agree on the API shape
//
// Property 2 (design.md): for any spec (or status) field present in either
// schema.yaml or definition.yaml, the field appears in both with the same name,
// nesting, type, enum value set, and default value — comparing exactly the
// attributes named in Requirement 9.2 and excluding only the CEL
// x-kubernetes-validations rules (the tenant pattern and the self == oldSelf
// immutability rules) that SimpleSchema cannot express. If any field diverges,
// the property fails and reports the diverging field, treating definition.yaml
// as authoritative.
//
// Validates: Requirements 9.1, 9.2, 9.3, 9.4, 9.5
package schema

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// fieldShape is the canonical, format-independent description of a single leaf
// field, extracted from either the XRD (OpenAPI) form or the SimpleSchema form.
// It captures exactly the attributes Requirement 9.2 compares: type, enum value
// set, and default. The CEL rules and the tenant OpenAPI pattern are excluded
// per Requirement 9.3.
type fieldShape struct {
	fieldType string   // OpenAPI type name: "string", "boolean", "object"
	enumSet   []string // sorted; nil when the field has no enum
	hasEnum   bool
	def       string // default rendered as a string; "" when absent
	hasDef    bool
}

// String renders a shape for diagnostics.
func (f fieldShape) String() string {
	return fmt.Sprintf("{type=%q enum=%v(present=%v) default=%q(present=%v)}",
		f.fieldType, f.enumSet, f.hasEnum, f.def, f.hasDef)
}

// equal compares two shapes on every attribute Requirement 9.2 names.
func (f fieldShape) equal(other fieldShape) bool {
	if f.fieldType != other.fieldType {
		return false
	}
	if f.hasEnum != other.hasEnum || f.hasDef != other.hasDef {
		return false
	}
	if f.def != other.def {
		return false
	}
	if len(f.enumSet) != len(other.enumSet) {
		return false
	}
	for i := range f.enumSet {
		if f.enumSet[i] != other.enumSet[i] {
			return false
		}
	}
	return true
}

// asString renders a scalar YAML value (bool/string/number) to the canonical
// string used for enum members and defaults, so that a boolean `true` in the
// XRD compares equal to the string `"true"` produced from SimpleSchema.
func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// normalizeDefinition walks the OpenAPI schema in definition.yaml and returns a
// canonical {fieldPath -> fieldShape} map for the union of spec and status leaf
// fields. Object containers (spec, status, bucket, table, repository) are
// recorded as type "object" so nesting mismatches are caught, but their
// enum/default are naturally absent. CEL x-kubernetes-validations and the
// tenant `pattern` are ignored (Req 9.3).
func normalizeDefinition(doc Document) (map[string]fieldShape, error) {
	out := map[string]fieldShape{}

	versions, ok := nestedSlice(doc, "spec", "versions")
	if !ok || len(versions) == 0 {
		return nil, fmt.Errorf("definition.yaml: spec.versions missing or empty")
	}
	v0, ok := versions[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("definition.yaml: spec.versions[0] is not a map")
	}
	root, ok := nestedMap(v0, "schema", "openAPIV3Schema", "properties")
	if !ok {
		return nil, fmt.Errorf("definition.yaml: openAPIV3Schema.properties missing")
	}

	for _, top := range []string{"spec", "status"} {
		sub, ok := root[top].(map[string]interface{})
		if !ok {
			continue
		}
		walkOpenAPI(top, sub, out)
	}
	return out, nil
}

// walkOpenAPI records the shape of the node at path, then recurses into its
// `properties`. Every node (leaf or object) is recorded so that a container
// present in one file but missing in the other is caught.
func walkOpenAPI(path string, node map[string]interface{}, out map[string]fieldShape) {
	shape := fieldShape{}
	if t, ok := node["type"].(string); ok {
		shape.fieldType = t
	}
	if raw, ok := node["enum"]; ok {
		if items, ok := raw.([]interface{}); ok {
			set := make([]string, 0, len(items))
			for _, it := range items {
				set = append(set, asString(it))
			}
			sort.Strings(set)
			shape.enumSet = set
			shape.hasEnum = true
		}
	}
	if raw, ok := node["default"]; ok {
		shape.def = asString(raw)
		shape.hasDef = true
	}
	out[path] = shape

	if props, ok := node["properties"].(map[string]interface{}); ok {
		for name, child := range props {
			if childMap, ok := child.(map[string]interface{}); ok {
				walkOpenAPI(path+"."+name, childMap, out)
			}
		}
	}
}

// normalizeSchema walks the SimpleSchema in schema.yaml and returns a canonical
// {fieldPath -> fieldShape} map for the union of spec and status leaf fields.
// Leaf fields are strings of the form `type | marker=value marker=value`;
// nested objects are nested maps. The type/enum/default markers are extracted;
// `required`, `description`, and any other marker are ignored (they are not
// part of the Req 9.2 comparison).
func normalizeSchema(doc Document) (map[string]fieldShape, error) {
	out := map[string]fieldShape{}
	found := false
	for _, top := range []string{"spec", "status"} {
		sub, ok := doc[top].(map[string]interface{})
		if !ok {
			continue
		}
		found = true
		// Container node, recorded as an object to mirror the XRD.
		out[top] = fieldShape{fieldType: "object"}
		walkSimpleSchema(top, sub, out)
	}
	if !found {
		return nil, fmt.Errorf("schema.yaml: neither spec nor status present")
	}
	return out, nil
}

// walkSimpleSchema recurses over a SimpleSchema object map. A map value is a
// nested object; a string value is a leaf `type | markers` declaration.
func walkSimpleSchema(path string, node map[string]interface{}, out map[string]fieldShape) {
	for name, raw := range node {
		childPath := path + "." + name
		switch v := raw.(type) {
		case map[string]interface{}:
			out[childPath] = fieldShape{fieldType: "object"}
			walkSimpleSchema(childPath, v, out)
		case string:
			out[childPath] = parseSimpleSchemaLeaf(v)
		}
	}
}

// parseSimpleSchemaLeaf parses a SimpleSchema field declaration such as
//
//	string | default="ap-southeast-1" enum="ap-southeast-1,ap-southeast-2"
//
// into a canonical fieldShape. The type is the token before the first `|`; the
// markers after it are parsed for `enum=` and `default=`. All other markers
// (required, description, ...) are ignored.
func parseSimpleSchemaLeaf(decl string) fieldShape {
	shape := fieldShape{}
	parts := strings.SplitN(decl, "|", 2)
	shape.fieldType = strings.TrimSpace(parts[0])
	if len(parts) < 2 {
		return shape
	}

	for key, val := range parseMarkers(parts[1]) {
		switch key {
		case "enum":
			set := strings.Split(val, ",")
			for i := range set {
				set[i] = strings.TrimSpace(set[i])
			}
			sort.Strings(set)
			shape.enumSet = set
			shape.hasEnum = true
		case "default":
			shape.def = val
			shape.hasDef = true
		}
	}
	return shape
}

// parseMarkers splits the marker portion of a SimpleSchema declaration into a
// key -> value map. Values may be quoted (`default="id"`) and may contain
// spaces inside quotes (`description="a b c"`). Unquoted values run to the next
// whitespace.
func parseMarkers(s string) map[string]string {
	markers := map[string]string{}
	runes := []rune(strings.TrimSpace(s))
	i := 0
	for i < len(runes) {
		// Skip leading whitespace.
		for i < len(runes) && isSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}
		// Read key up to '='.
		keyStart := i
		for i < len(runes) && runes[i] != '=' && !isSpace(runes[i]) {
			i++
		}
		key := string(runes[keyStart:i])
		if i >= len(runes) || runes[i] != '=' {
			// A bare marker with no value; skip it.
			continue
		}
		i++ // consume '='
		var val string
		if i < len(runes) && runes[i] == '"' {
			i++ // consume opening quote
			valStart := i
			for i < len(runes) && runes[i] != '"' {
				i++
			}
			val = string(runes[valStart:i])
			if i < len(runes) {
				i++ // consume closing quote
			}
		} else {
			valStart := i
			for i < len(runes) && !isSpace(runes[i]) {
				i++
			}
			val = string(runes[valStart:i])
		}
		markers[key] = val
	}
	return markers
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// nestedMap descends the given keys through nested map[string]interface{}s.
func nestedMap(m map[string]interface{}, keys ...string) (map[string]interface{}, bool) {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// nestedSlice descends keys[:len-1] as maps, then returns keys[len-1] as a slice.
func nestedSlice(m map[string]interface{}, keys ...string) ([]interface{}, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	parent := m
	if len(keys) > 1 {
		var ok bool
		parent, ok = nestedMap(m, keys[:len(keys)-1]...)
		if !ok {
			return nil, false
		}
	}
	s, ok := parent[keys[len(keys)-1]].([]interface{})
	return s, ok
}

// unionPaths returns the sorted union of field paths from both maps.
func unionPaths(a, b map[string]fieldShape) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for k := range seen {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	return paths
}

// loadNormalized loads and normalizes both artifacts, failing the test on any
// load/parse error.
func loadNormalized(t *testing.T) (defShapes, schemaShapes map[string]fieldShape) {
	t.Helper()
	defDoc, err := LoadDefinition()
	if err != nil {
		t.Fatalf("load definition.yaml: %v", err)
	}
	schemaDoc, err := LoadSchema()
	if err != nil {
		t.Fatalf("load schema.yaml: %v", err)
	}
	defShapes, err = normalizeDefinition(defDoc)
	if err != nil {
		t.Fatalf("normalize definition.yaml: %v", err)
	}
	schemaShapes, err = normalizeSchema(schemaDoc)
	if err != nil {
		t.Fatalf("normalize schema.yaml: %v", err)
	}
	return defShapes, schemaShapes
}

// TestParitySchemaDefinition is the property test for Property 2. It walks the
// union of field paths in a randomized order (rapid selects the visit order and
// the field to inspect each iteration, 100+ iterations) and asserts that every
// field agrees on name/nesting/type/enum-set/default between the two files,
// treating definition.yaml as authoritative.
//
// Validates: Requirements 9.1, 9.2, 9.3, 9.4, 9.5
func TestParitySchemaDefinition(t *testing.T) {
	defShapes, schemaShapes := loadNormalized(t)

	paths := unionPaths(defShapes, schemaShapes)
	if len(paths) == 0 {
		t.Fatal("no fields extracted from either artifact")
	}

	rapid.Check(t, func(rt *rapid.T) {
		// Randomize the field-visit order (Req 9.4 reports the diverging field;
		// randomizing order ensures no field is favored). rapid drives 100+
		// iterations by default.
		idx := rapid.IntRange(0, len(paths)-1).Draw(rt, "fieldIndex")
		path := paths[idx]

		defShape, inDef := defShapes[path]
		schemaShape, inSchema := schemaShapes[path]

		// Req 9.1: neither file may contain a field absent from the other.
		if inDef != inSchema {
			present, missing := "definition.yaml", "schema.yaml"
			if !inDef {
				present, missing = "schema.yaml", "definition.yaml"
			}
			rt.Fatalf("field %q present in %s but missing from %s", path, present, missing)
		}

		// Req 9.2 / 9.4: agree on type, enum set, and default. definition.yaml
		// is authoritative, so it is reported as the expected value.
		if !defShape.equal(schemaShape) {
			rt.Fatalf("field %q diverges:\n  definition.yaml (authoritative): %s\n  schema.yaml:                    %s",
				path, defShape, schemaShape)
		}
	})
}

// TestParityDetectsInjectedDrift is a sanity sub-test: it mutates a copy of the
// normalized schema map and confirms the same comparison the property relies on
// reports the drift. This guards the checker itself against silently passing.
func TestParityDetectsInjectedDrift(t *testing.T) {
	defShapes, schemaShapes := loadNormalized(t)

	cases := map[string]func(m map[string]fieldShape){
		"changed type": func(m map[string]fieldShape) {
			s := m["spec.bucket.versioning"]
			s.fieldType = "string"
			m["spec.bucket.versioning"] = s
		},
		"changed default": func(m map[string]fieldShape) {
			s := m["spec.region"]
			s.def = "us-west-2"
			m["spec.region"] = s
		},
		"changed enum set": func(m map[string]fieldShape) {
			s := m["spec.table.billingMode"]
			s.enumSet = []string{"PAY_PER_REQUEST"}
			m["spec.table.billingMode"] = s
		},
		"removed field": func(m map[string]fieldShape) {
			delete(m, "spec.repository.enabled")
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := make(map[string]fieldShape, len(schemaShapes))
			for k, v := range schemaShapes {
				mutated[k] = v
			}
			mutate(mutated)

			if parityHolds(defShapes, mutated) {
				t.Fatalf("injected drift %q was not detected: the parity check still passed", name)
			}
		})
	}
}

// parityHolds returns true when every field in the union agrees. It mirrors the
// assertions in the property test so the drift sub-test exercises the same
// logic.
func parityHolds(a, b map[string]fieldShape) bool {
	for _, path := range unionPaths(a, b) {
		sa, inA := a[path]
		sb, inB := b[path]
		if inA != inB {
			return false
		}
		if !sa.equal(sb) {
			return false
		}
	}
	return true
}
