package main

import (
	"testing"

	"pgregory.net/rapid"
)

// p4BlankStrings are the whitespace-only (and empty) building blocks a blank
// tenant/environment can be assembled from.
var p4BlankStrings = []string{"", " ", "\t", "\n", "  ", " \t", "\t\n ", "\n\n", "   \t  "}

// p4ValidTokens are non-blank strings used for the "other" field when only one
// side is blank, so the test still exercises the single-blank case.
var p4ValidTokens = []string{"acme", "globex", "dev", "prod", "staging", "t1"}

// p4GenBlank draws a value that is empty or whitespace-only.
func p4GenBlank(t *rapid.T, label string) string {
	return rapid.SampledFrom(p4BlankStrings).Draw(t, label)
}

// p4GenMaybeBlank draws a value that is either blank or a valid token.
func p4GenMaybeBlank(t *rapid.T, label string) string {
	if rapid.Bool().Draw(t, label+"-isBlank") {
		return p4GenBlank(t, label+"-blank")
	}
	return rapid.SampledFrom(p4ValidTokens).Draw(t, label+"-token")
}

// TestPropertyNamingRejectsBlankInput asserts that BuildNames refuses any input
// where tenant OR environment is empty or whitespace-only: it must return a
// non-nil error and a zero-value Names{} (no names).
//
// Feature: tenant-environment-s3, Property 4: Naming rejects empty or whitespace input
// Validates: Requirements 2.7, 3.6
func TestPropertyNamingRejectsBlankInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Ensure at least one of the two fields is blank; the other may be
		// blank or a valid token.
		var tenant, environment string
		if rapid.Bool().Draw(t, "tenantBlank") {
			tenant = p4GenBlank(t, "tenant")
			environment = p4GenMaybeBlank(t, "environment")
		} else {
			// tenant is not the forced-blank field, so environment must be blank.
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
	})
}
