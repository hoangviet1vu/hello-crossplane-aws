package schema

import (
	"testing"
)

// specProperties navigates to
// spec.versions[0].schema.openAPIV3Schema.properties.spec.properties, where the
// CEL immutability rules live on the tenant and environment field schemas.
// Named distinctly from statusProperties to avoid a collision with the existing
// helper in xrd_identity_test.go.
func specProperties(t *testing.T, doc Document) map[string]interface{} {
	t.Helper()
	version := firstVersion(t, doc)
	schema := asMap(t, version["schema"], "spec.versions[0].schema")
	openAPI := asMap(t, schema["openAPIV3Schema"], "openAPIV3Schema")
	props := asMap(t, openAPI["properties"], "openAPIV3Schema.properties")
	spec := asMap(t, props["spec"], "openAPIV3Schema.properties.spec")
	return asMap(t, spec["properties"], "spec.properties")
}

// firstValidation extracts the single x-kubernetes-validations entry from a
// field schema, failing if the field is missing the rule block or does not
// carry exactly one rule.
func firstValidation(t *testing.T, fieldSchema map[string]interface{}, field string) map[string]interface{} {
	t.Helper()
	raw, present := fieldSchema["x-kubernetes-validations"]
	if !present {
		t.Fatalf("spec.properties.%s: missing x-kubernetes-validations (an xrd generate re-run may have wiped the CEL rule)", field)
	}
	list, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("spec.properties.%s.x-kubernetes-validations: expected list, got %T", field, raw)
	}
	if len(list) != 1 {
		t.Fatalf("spec.properties.%s.x-kubernetes-validations: expected exactly 1 rule, got %d", field, len(list))
	}
	return asMap(t, list[0], "spec.properties."+field+".x-kubernetes-validations[0]")
}

// TestCELImmutabilityRules asserts the two self == oldSelf transition rules are
// present on spec.tenant and spec.environment with their exact messages. This
// is the regression guard against an accidental `crossplane xrd generate`
// re-run, which does not emit CEL x-kubernetes-validations and would silently
// delete these rules. Requirements: 3.1, 3.2.
func TestCELImmutabilityRules(t *testing.T) {
	doc := loadDefinitionOrFatal(t)
	specProps := specProperties(t, doc)

	cases := map[string]struct {
		field       string
		wantRule    string
		wantMessage string
	}{
		"tenant is immutable": {
			field:       "tenant",
			wantRule:    "self == oldSelf",
			wantMessage: "tenant is immutable",
		},
		"environment is immutable": {
			field:       "environment",
			wantRule:    "self == oldSelf",
			wantMessage: "environment is immutable",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fieldSchema := asMap(t, specProps[tc.field], "spec.properties."+tc.field)
			validation := firstValidation(t, fieldSchema, tc.field)

			if got := validation["rule"]; got != tc.wantRule {
				t.Errorf("spec.properties.%s CEL rule: got %#v, want %#v", tc.field, got, tc.wantRule)
			}
			if got := validation["message"]; got != tc.wantMessage {
				t.Errorf("spec.properties.%s CEL message: got %#v, want %#v", tc.field, got, tc.wantMessage)
			}
		})
	}
}
