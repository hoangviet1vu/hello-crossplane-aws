package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
)

// pddb8Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb8Environments = []string{"dev", "staging", "prod"}

// pddb8Regions is the XRD region allow-list.
var pddb8Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb8ManagedBy is the fixed value of the "managed-by" tag applied to the
// composed Table.
const pddb8ManagedBy = "crossplane"

// pddb8TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb8TenantGen() *rapid.Generator[string] {
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

// pddb8Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// table.enabled is varied so the property checks both the enabled (tag set
// present and exact) and disabled (table key absent) behaviors. repository.enabled
// and the S3 field vary freely.
type pddb8Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repoEnabled  bool
}

// pddb8GenSpec draws a valid TenantEnvironment spec, varying table.enabled (and
// repository.enabled, S3 fields) across the input space.
func pddb8GenSpec(t *rapid.T) pddb8Spec {
	return pddb8Spec{
		tenant:       pddb8TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(pddb8Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(pddb8Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repoEnabled:  rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// pddb8BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec.
func pddb8BuildRequest(s pddb8Spec) (*fnv1.RunFunctionRequest, error) {
	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name":      s.tenant + "-" + s.environment,
			"namespace": s.tenant + "-" + s.environment,
		},
		"spec": map[string]any{
			"tenant":      s.tenant,
			"environment": s.environment,
			"region":      s.region,
			"bucket": map[string]any{
				"versioning": s.versioning,
			},
			"table": map[string]any{
				"enabled": s.tableEnabled,
			},
			"repository": map[string]any{
				"enabled": s.repoEnabled,
			},
		},
	}

	xrStruct, err := structpb.NewStruct(xr)
	if err != nil {
		return nil, err
	}

	return &fnv1.RunFunctionRequest{
		Observed: &fnv1.State{
			Composite: &fnv1.Resource{
				Resource: xrStruct,
			},
		},
	}, nil
}

// pddb8ReadTags reads spec.forProvider.tags off a desired composed resource's
// unstructured content, returning the tag map as a string->string map. A missing
// or malformed tags field yields an empty map, which the assertion below then
// reports as a key-set mismatch. A non-string tag value is surfaced with a
// sentinel so the exact-key/value comparison fails.
func pddb8ReadTags(dcd *resource.DesiredComposed) map[string]string {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	raw, ok := forProvider["tags"].(map[string]any)
	if !ok {
		return map[string]string{}
	}

	tags := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			tags[k] = "<non-string>"
			continue
		}
		tags[k] = s
	}
	return tags
}

// Feature: tenant-environment-dynamodb, Property 8: Table tag set is exactly the three standard tags
//
// For any valid TenantEnvironment spec with table.enabled: true, the composed
// "table" desired resource carries spec.forProvider.tags with EXACTLY the keys
// {environment, managed-by, tenant} — no more, no fewer — with tenant/environment
// equal to the XR spec fields and managed-by == crossplane. When table.enabled is
// false, no "table" key is emitted at all (so no such tags exist).
//
// Validates: Requirements 5.1, 5.2, 5.3, 5.4, 7.9
func TestPropertyDDB8TableTagSet(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb8GenSpec(t)

		req, err := pddb8BuildRequest(s)
		if err != nil {
			t.Fatalf("pddb8BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		table, ok := dcds[keyTable]

		if !s.tableEnabled {
			// Disabled: the table key must be absent entirely, so no Table tags
			// exist.
			if ok {
				t.Fatalf("table.enabled=false but desired resources contain a %q key (spec=%+v)", keyTable, s)
			}
			return
		}

		// Enabled: the table key must exist and carry exactly the three standard
		// tags.
		if !ok {
			t.Fatalf("table.enabled=true but desired resources missing %q key (spec=%+v)", keyTable, s)
		}

		want := map[string]string{
			"tenant":      s.tenant,
			"environment": s.environment,
			"managed-by":  pddb8ManagedBy,
		}
		got := pddb8ReadTags(table)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("table spec.forProvider.tags mismatch (-want +got):\n%s\n(spec=%+v)", diff, s)
		}
	})
}
