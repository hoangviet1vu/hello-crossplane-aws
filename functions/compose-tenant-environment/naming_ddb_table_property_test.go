package main

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: tenant-environment-dynamodb, Property 10: Naming is deterministic
// and rejects empty inputs.
//
// For any valid tenant/environment, BuildNames yields a byte-identical
// TableName equal to "<tenant>-<env>-dtbl" on repeated invocations. For any
// empty or whitespace-only tenant/environment, BuildNames returns a non-nil
// error and a zero-value Names (so TableName == "").
//
// Validates: Requirements 3.1, 3.2, 3.5
func TestProperty10TableNameDeterministicAndRejectsBlank(t *testing.T) {
	// Valid inputs: TableName is deterministic and equals "<tenant>-<env>-dtbl".
	t.Run("valid/deterministic", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			tenant := p3TenantGenerator().Draw(t, "tenant")
			environment := rapid.SampledFrom(p3Environments).Draw(t, "environment")

			first, err := BuildNames(tenant, environment)
			if err != nil {
				t.Fatalf("BuildNames(%q, %q) unexpectedly returned error: %v", tenant, environment, err)
			}

			want := tenant + "-" + environment + "-dtbl"
			if first.TableName != want {
				t.Fatalf("BuildNames(%q, %q) TableName = %q; want %q",
					tenant, environment, first.TableName, want)
			}

			// Repeat the derivation and assert byte-identical TableName.
			for i := 0; i < 5; i++ {
				got, err := BuildNames(tenant, environment)
				if err != nil {
					t.Fatalf("BuildNames(%q, %q) call %d returned error: %v", tenant, environment, i, err)
				}
				if got.TableName != first.TableName {
					t.Fatalf("BuildNames(%q, %q) TableName not deterministic: call 0 = %q, call %d = %q",
						tenant, environment, first.TableName, i, got.TableName)
				}
			}
		})
	})

	// Blank inputs: BuildNames rejects with an error and a zero-value Names,
	// so TableName is the empty string.
	t.Run("blank/rejected", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			var tenant, environment string
			if rapid.Bool().Draw(t, "tenantBlank") {
				tenant = p4GenBlank(t, "tenant")
				environment = p4GenMaybeBlank(t, "environment")
			} else {
				tenant = p4GenMaybeBlank(t, "tenant")
				environment = p4GenBlank(t, "environment")
			}

			got, err := BuildNames(tenant, environment)
			if err == nil {
				t.Fatalf("BuildNames(%q, %q) = %+v, nil; want non-nil error for blank input", tenant, environment, got)
			}
			if got != (Names{}) {
				t.Fatalf("BuildNames(%q, %q) returned names %+v; want zero-value Names{} on error", tenant, environment, got)
			}
			if got.TableName != "" {
				t.Fatalf("BuildNames(%q, %q) TableName = %q; want empty string on error", tenant, environment, got.TableName)
			}
		})
	})
}
