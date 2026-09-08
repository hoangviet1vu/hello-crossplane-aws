package schema

import (
	"reflect"
	"testing"
)

// loadDefinitionOrFatal loads definition.yaml once for the identity/status
// assertions, failing the test immediately if the authoritative XRD cannot be
// read or parsed.
func loadDefinitionOrFatal(t *testing.T) Document {
	t.Helper()
	doc, err := LoadDefinition()
	if err != nil {
		t.Fatalf("load definition.yaml: %v", err)
	}
	return doc
}

// asMap coerces a parsed YAML node to map[string]interface{}, failing the test
// with a descriptive path if the node is missing or the wrong shape.
func asMap(t *testing.T, v interface{}, path string) map[string]interface{} {
	t.Helper()
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("%s: expected object, got %T (%v)", path, v, v)
	}
	return m
}

// firstVersion returns the single entry in spec.versions, failing if there is
// not exactly one.
func firstVersion(t *testing.T, doc Document) map[string]interface{} {
	t.Helper()
	spec := asMap(t, doc["spec"], "spec")
	versionsRaw, ok := spec["versions"].([]interface{})
	if !ok {
		t.Fatalf("spec.versions: expected list, got %T", spec["versions"])
	}
	if len(versionsRaw) != 1 {
		t.Fatalf("spec.versions: expected exactly 1 version, got %d", len(versionsRaw))
	}
	return asMap(t, versionsRaw[0], "spec.versions[0]")
}

// TestXRDIdentity asserts the top-level XRD identity fields: apiVersion, kind,
// group, names, scope, and the absence of any claim configuration.
// Requirements: 1.1, 1.2, 1.3, 1.5, 1.6.
func TestXRDIdentity(t *testing.T) {
	doc := loadDefinitionOrFatal(t)
	spec := asMap(t, doc["spec"], "spec")
	names := asMap(t, spec["names"], "spec.names")

	cases := map[string]struct {
		got  interface{}
		want interface{}
	}{
		"apiVersion is v2": {
			got:  doc["apiVersion"],
			want: "apiextensions.crossplane.io/v2",
		},
		"kind is CompositeResourceDefinition": {
			got:  doc["kind"],
			want: "CompositeResourceDefinition",
		},
		"group": {
			got:  spec["group"],
			want: "platform.hello-crossplane.io",
		},
		"scope is Namespaced": {
			got:  spec["scope"],
			want: "Namespaced",
		},
		"names.kind": {
			got:  names["kind"],
			want: "TenantEnvironment",
		},
		"names.plural": {
			got:  names["plural"],
			want: "tenantenvironments",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("got %#v, want %#v", tc.got, tc.want)
			}
		})
	}
}

// TestXRDNoClaimNames asserts the XRD carries no claim configuration anywhere:
// a Crossplane v2 namespaced XRD replaces claims, so claimNames must be absent
// from both the document root and spec. Requirement: 1.6.
func TestXRDNoClaimNames(t *testing.T) {
	doc := loadDefinitionOrFatal(t)
	spec := asMap(t, doc["spec"], "spec")

	cases := map[string]struct {
		container map[string]interface{}
	}{
		"root has no claimNames": {container: map[string]interface{}(doc)},
		"spec has no claimNames": {container: spec},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if v, present := tc.container["claimNames"]; present {
				t.Errorf("claimNames must be absent, found %#v", v)
			}
		})
	}
}

// TestXRDVersion asserts there is exactly one version, named v1alpha1, served
// and referenceable. Requirement: 1.4.
func TestXRDVersion(t *testing.T) {
	doc := loadDefinitionOrFatal(t)
	version := firstVersion(t, doc)

	cases := map[string]struct {
		got  interface{}
		want interface{}
	}{
		"name is v1alpha1": {
			got:  version["name"],
			want: "v1alpha1",
		},
		"served is true": {
			got:  version["served"],
			want: true,
		},
		"referenceable is true": {
			got:  version["referenceable"],
			want: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("got %#v, want %#v", tc.got, tc.want)
			}
		})
	}
}

// statusProperties navigates to
// spec.versions[0].schema.openAPIV3Schema.properties.status.properties, the
// place a v2 XRD declares its status schema (the status subresource is implicit
// once this schema is present). Requirement: 8.1.
func statusProperties(t *testing.T, doc Document) map[string]interface{} {
	t.Helper()
	version := firstVersion(t, doc)
	schema := asMap(t, version["schema"], "spec.versions[0].schema")
	openAPI := asMap(t, schema["openAPIV3Schema"], "openAPIV3Schema")

	if got := openAPI["type"]; got != "object" {
		t.Fatalf("openAPIV3Schema.type: got %#v, want \"object\"", got)
	}

	props := asMap(t, openAPI["properties"], "openAPIV3Schema.properties")
	status := asMap(t, props["status"], "openAPIV3Schema.properties.status")

	if got := status["type"]; got != "object" {
		t.Fatalf("status.type: got %#v, want \"object\"", got)
	}
	// A v2 XRD enables the status subresource implicitly by declaring the
	// status schema; assert it exists rather than looking for a subresources
	// stanza (which a raw CRD would use but an XRD does not).
	return asMap(t, status["properties"], "status.properties")
}

// TestXRDStatusFields asserts the three reserved status fields are declared as
// strings. Requirements: 8.1, 8.2, 8.3, 8.4.
func TestXRDStatusFields(t *testing.T) {
	doc := loadDefinitionOrFatal(t)
	statusProps := statusProperties(t, doc)

	cases := map[string]struct {
		field string
	}{
		"bucketName is a string":    {field: "bucketName"},
		"tableName is a string":     {field: "tableName"},
		"repositoryUrl is a string": {field: "repositoryUrl"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fieldSchema := asMap(t, statusProps[tc.field], "status.properties."+tc.field)
			if got := fieldSchema["type"]; got != "string" {
				t.Errorf("status.%s.type: got %#v, want \"string\"", tc.field, got)
			}
		})
	}
}

// TestXRDStatusNotRequired asserts the status schema declares no required list,
// so none of the reserved fields are required on create. Requirements: 8.2,
// 8.3, 8.4.
func TestXRDStatusNotRequired(t *testing.T) {
	doc := loadDefinitionOrFatal(t)
	version := firstVersion(t, doc)
	schema := asMap(t, version["schema"], "spec.versions[0].schema")
	openAPI := asMap(t, schema["openAPIV3Schema"], "openAPIV3Schema")
	props := asMap(t, openAPI["properties"], "openAPIV3Schema.properties")
	status := asMap(t, props["status"], "openAPIV3Schema.properties.status")

	required, present := status["required"]
	if !present {
		return // no required list at all: nothing is required, as intended.
	}

	requiredList, ok := required.([]interface{})
	if !ok {
		t.Fatalf("status.required: expected list, got %T", required)
	}
	if len(requiredList) != 0 {
		t.Errorf("status.required must be empty, got %#v", requiredList)
	}
}
