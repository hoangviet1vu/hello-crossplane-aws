// Package main tests for the pure naming and mapping logic in naming.go.
// These are example-based, table-driven unit tests (map[string]struct{...} +
// t.Run, compared with go-cmp) complementing the property-based tests that
// live in sibling files. They satisfy R8.3's "at least one table-driven test
// exercising the naming module".
package main

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// shortestTenant is the shortest tenant the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ allows: a leading char, a single middle
// char, and a trailing char => 3 characters.
const shortestTenant = "abc"

// longestTenant is the longest tenant the pattern allows: leading char + 20
// middle chars + trailing char => 22 characters.
const longestTenant = "a12345678901234567890b"

func TestBuildNames(t *testing.T) {
	cases := map[string]struct {
		tenant      string
		environment string
		want        Names
		wantErr     bool
	}{
		"shortest tenant, dev": {
			tenant:      shortestTenant,
			environment: "dev",
			want: Names{
				Namespace:      "abc-dev",
				BucketName:     "abc-dev-bucket",
				RepositoryName: "abc-dev-ecr",
				TableName:      "abc-dev-dtbl",
			},
		},
		"longest tenant, prod": {
			tenant:      longestTenant,
			environment: "prod",
			want: Names{
				Namespace:      "a12345678901234567890b-prod",
				BucketName:     "a12345678901234567890b-prod-bucket",
				RepositoryName: "a12345678901234567890b-prod-ecr",
				TableName:      "a12345678901234567890b-prod-dtbl",
			},
		},
		"environment dev": {
			tenant:      "acme",
			environment: "dev",
			want: Names{
				Namespace:      "acme-dev",
				BucketName:     "acme-dev-bucket",
				RepositoryName: "acme-dev-ecr",
				TableName:      "acme-dev-dtbl",
			},
		},
		"environment staging": {
			tenant:      "acme",
			environment: "staging",
			want: Names{
				Namespace:      "acme-staging",
				BucketName:     "acme-staging-bucket",
				RepositoryName: "acme-staging-ecr",
				TableName:      "acme-staging-dtbl",
			},
		},
		"environment prod": {
			tenant:      "acme",
			environment: "prod",
			want: Names{
				Namespace:      "acme-prod",
				BucketName:     "acme-prod-bucket",
				RepositoryName: "acme-prod-ecr",
				TableName:      "acme-prod-dtbl",
			},
		},
		"empty tenant": {
			tenant:      "",
			environment: "dev",
			want:        Names{},
			wantErr:     true,
		},
		"whitespace tenant": {
			tenant:      "   ",
			environment: "dev",
			want:        Names{},
			wantErr:     true,
		},
		"tab/newline tenant": {
			tenant:      "\t\n",
			environment: "dev",
			want:        Names{},
			wantErr:     true,
		},
		"empty environment": {
			tenant:      "acme",
			environment: "",
			want:        Names{},
			wantErr:     true,
		},
		"whitespace environment": {
			tenant:      "acme",
			environment: "   ",
			want:        Names{},
			wantErr:     true,
		},
		"both empty": {
			tenant:      "",
			environment: "",
			want:        Names{},
			wantErr:     true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := BuildNames(tc.tenant, tc.environment)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("BuildNames(%q, %q): expected an error, got nil", tc.tenant, tc.environment)
				}
			} else if err != nil {
				t.Fatalf("BuildNames(%q, %q): unexpected error: %v", tc.tenant, tc.environment, err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("BuildNames(%q, %q) Names mismatch (-want +got):\n%s", tc.tenant, tc.environment, diff)
			}
		})
	}
}

func TestVersioningStatus(t *testing.T) {
	cases := map[string]struct {
		enabled bool
		want    string
	}{
		"enabled true maps to Enabled": {
			enabled: true,
			want:    "Enabled",
		},
		"enabled false maps to Suspended": {
			enabled: false,
			want:    "Suspended",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := VersioningStatus(tc.enabled)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("VersioningStatus(%v) mismatch (-want +got):\n%s", tc.enabled, diff)
			}
		})
	}
}

func TestStandardTags(t *testing.T) {
	cases := map[string]struct {
		tenant      string
		environment string
		want        map[string]string
	}{
		"dev tags": {
			tenant:      "acme",
			environment: "dev",
			want: map[string]string{
				"tenant":      "acme",
				"environment": "dev",
				"managed-by":  "crossplane",
			},
		},
		"prod tags": {
			tenant:      "globex",
			environment: "prod",
			want: map[string]string{
				"tenant":      "globex",
				"environment": "prod",
				"managed-by":  "crossplane",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := StandardTags(tc.tenant, tc.environment)

			// Full value comparison: keys and values must match exactly.
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("StandardTags(%q, %q) mismatch (-want +got):\n%s", tc.tenant, tc.environment, diff)
			}

			// Explicitly assert the exact key set — no additional tag keys
			// beyond {tenant, environment, managed-by} (R6.5).
			wantKeys := []string{"environment", "managed-by", "tenant"}
			gotKeys := make([]string, 0, len(got))
			for k := range got {
				gotKeys = append(gotKeys, k)
			}
			sort.Strings(gotKeys)
			if diff := cmp.Diff(wantKeys, gotKeys); diff != "" {
				t.Errorf("StandardTags(%q, %q) key set mismatch (-want +got):\n%s", tc.tenant, tc.environment, diff)
			}
		})
	}
}
