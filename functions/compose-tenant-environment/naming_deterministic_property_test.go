package main

import (
	"testing"

	"pgregory.net/rapid"
)

// p3Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p3Environments = []string{"dev", "staging", "prod"}

// p3TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between,
// for a total length of 3–22 characters.
func p3TenantGenerator() *rapid.Generator[string] {
	edge := rapid.SampledFrom([]byte("abcdefghijklmnopqrstuvwxyz0123456789"))
	middle := rapid.SampledFrom([]byte("abcdefghijklmnopqrstuvwxyz0123456789-"))
	return rapid.Custom(func(t *rapid.T) string {
		first := edge.Draw(t, "first")
		last := edge.Draw(t, "last")
		mid := rapid.SliceOfN(middle, 1, 20).Draw(t, "middle")
		b := make([]byte, 0, len(mid)+2)
		b = append(b, first)
		b = append(b, mid...)
		b = append(b, last)
		return string(b)
	})
}

// Feature: tenant-environment-s3, Property 3: Name derivation is deterministic
//
// For any valid tenant and environment, invoking BuildNames repeatedly produces
// a byte-identical BucketName (and Namespace) every time.
//
// Validates: Requirements 3.2
func TestProperty3NameDerivationIsDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tenant := p3TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(p3Environments).Draw(t, "environment")

		first, err := BuildNames(tenant, environment)
		if err != nil {
			t.Fatalf("BuildNames(%q, %q) unexpectedly returned error: %v", tenant, environment, err)
		}

		// Repeat the derivation several times and assert byte-identical output.
		for i := 0; i < 5; i++ {
			got, err := BuildNames(tenant, environment)
			if err != nil {
				t.Fatalf("BuildNames(%q, %q) call %d returned error: %v", tenant, environment, i, err)
			}
			if got.BucketName != first.BucketName {
				t.Fatalf("BuildNames(%q, %q) BucketName not deterministic: call 0 = %q, call %d = %q",
					tenant, environment, first.BucketName, i, got.BucketName)
			}
			if got.Namespace != first.Namespace {
				t.Fatalf("BuildNames(%q, %q) Namespace not deterministic: call 0 = %q, call %d = %q",
					tenant, environment, first.Namespace, i, got.Namespace)
			}
		}
	})
}
