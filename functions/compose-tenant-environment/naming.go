// Package main contains the embedded composition function for
// TenantEnvironment. This file holds the pure naming and mapping logic and
// deliberately imports no Crossplane packages, so it is trivially unit-testable
// in isolation (R8.5).
package main

import (
	"fmt"
	"strings"
)

// managedBy is the fixed value of the "managed-by" tag applied to every
// composed resource.
const managedBy = "crossplane"

// Names holds the deterministic identifiers derived from a TenantEnvironment.
type Names struct {
	Namespace  string // "<tenant>-<env>"
	BucketName string // "<tenant>-<env>-bucket"  (the AWS external name)
}

// BuildNames validates tenant and environment and returns the derived names.
// It returns an error and a zero-value Names if either input is empty or
// whitespace-only, so the caller can refuse to emit resources with a malformed
// external name (R3.6, R2.7). For identical inputs it always yields a
// byte-identical result — no time, randomness, or map iteration (R3.2).
func BuildNames(tenant, environment string) (Names, error) {
	if strings.TrimSpace(tenant) == "" {
		return Names{}, fmt.Errorf("tenant is empty or whitespace-only")
	}
	if strings.TrimSpace(environment) == "" {
		return Names{}, fmt.Errorf("environment is empty or whitespace-only")
	}

	namespace := tenant + "-" + environment
	return Names{
		Namespace:  namespace,
		BucketName: namespace + "-bucket",
	}, nil
}

// VersioningStatus maps the spec flag to the provider enum value.
// true → "Enabled", false → "Suspended" (R4.3, R4.4).
func VersioningStatus(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Suspended"
}

// StandardTags returns the fixed tag set for every composed resource.
// Exactly the keys {tenant, environment, managed-by} and no others (R6.5).
func StandardTags(tenant, environment string) map[string]string {
	return map[string]string{
		"tenant":      tenant,
		"environment": environment,
		"managed-by":  managedBy,
	}
}
