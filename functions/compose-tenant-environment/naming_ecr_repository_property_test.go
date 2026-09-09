package main

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: tenant-environment-ecr, Property 8: Naming is deterministic and
// rejects empty inputs.
//
// For any valid tenant/environment, BuildNames yields a byte-identical
// RepositoryName equal to "<tenant>-<env>-ecr" on repeated invocations. For any
// empty or whitespace-only tenant/environment, BuildNames returns a non-nil
// error and a zero-value Names (so RepositoryName == "").
//
// Validates: Requirements 3.1, 3.2, 3.5
func TestProperty8RepositoryNameDeterministicAndRejectsBlank(t *testing.T) {
	// Valid inputs: RepositoryName is deterministic and equals "<tenant>-<env>-ecr".
	t.Run("valid/deterministic", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			tenant := p3TenantGenerator().Draw(t, "tenant")
			environment := rapid.SampledFrom(p3Environments).Draw(t, "environment")

			first, err := BuildNames(tenant, environment)
			if err != nil {
				t.Fatalf("BuildNames(%q, %q) unexpectedly returned error: %v", tenant, environment, err)
			}

			want := tenant + "-" + environment + "-ecr"
			if first.RepositoryName != want {
				t.Fatalf("BuildNames(%q, %q) RepositoryName = %q; want %q",
					tenant, environment, first.RepositoryName, want)
			}

			// Repeat the derivation and assert byte-identical RepositoryName.
			for i := 0; i < 5; i++ {
				got, err := BuildNames(tenant, environment)
				if err != nil {
					t.Fatalf("BuildNames(%q, %q) call %d returned error: %v", tenant, environment, i, err)
				}
				if got.RepositoryName != first.RepositoryName {
					t.Fatalf("BuildNames(%q, %q) RepositoryName not deterministic: call 0 = %q, call %d = %q",
						tenant, environment, first.RepositoryName, i, got.RepositoryName)
				}
			}
		})
	})

	// Blank inputs: BuildNames rejects with an error and a zero-value Names,
	// so RepositoryName is the empty string.
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
			if got.RepositoryName != "" {
				t.Fatalf("BuildNames(%q, %q) RepositoryName = %q; want empty string on error", tenant, environment, got.RepositoryName)
			}
		})
	})
}
