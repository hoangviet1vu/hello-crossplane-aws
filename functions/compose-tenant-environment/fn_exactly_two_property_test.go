package main

import (
	"context"
	"sort"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
)

// p8Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p8Environments = []string{"dev", "staging", "prod"}

// p8Regions is the XRD region allow-list. Any of these is valid for spec.region.
var p8Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// p8ExpectedKeys is the exact key set the desired-resources map must contain,
// regardless of the table/repository enabled flags. This slice of the feature
// composes only the S3 bucket and its versioning resource.
var p8ExpectedKeys = []resource.Name{keyBucket, keyBucketVersioning}

// p8TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func p8TenantGen() *rapid.Generator[string] {
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

// p8Spec captures the drawn TenantEnvironment spec inputs for one iteration.
type p8Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repoEnabled  bool
}

// p8GenSpec draws a full valid TenantEnvironment spec, varying table.enabled
// (as well as tenant, environment, region, and bucket.versioning) so the
// property is exercised across the input space. repository.enabled is pinned
// FALSE here: this property asserts the two-resource S3-slice invariant, which
// only holds while ECR is disabled. The enabled case (three resources) is
// covered by P-ECR-1 in fn_ecr_keyset_property_test.go.
func p8GenSpec(t *rapid.T) p8Spec {
	return p8Spec{
		tenant:       p8TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(p8Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(p8Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repoEnabled:  false,
	}
}

// p8BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment (apiVersion platform.hello-crossplane.io/
// v1alpha1, kind TenantEnvironment) carrying the given spec.
func p8BuildRequest(s p8Spec) (*fnv1.RunFunctionRequest, error) {
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

// p8Keys returns the sorted string form of a desired-composed key set for
// stable comparison and readable failure messages.
func p8Keys(resources map[resource.Name]*resource.DesiredComposed) []string {
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}

// Feature: tenant-environment-s3, Property 8: Exactly two resources are composed
//
// For any valid TenantEnvironment spec — with table.enabled taking any value
// and repository.enabled FALSE — RunFunction always returns a desired composed
// resources map whose key set is EXACTLY {"bucket", "bucket-versioning"}. No
// Table, no Repository, no other key is emitted for this S3-slice invariant.
// The repository.enabled=true case (three resources) is covered by P-ECR-1 in
// fn_ecr_keyset_property_test.go.
//
// Validates: Requirements 4.1, 6.1, 6.2, 6.3
func TestProperty8ExactlyTwoResources(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := p8GenSpec(t)

		req, err := p8BuildRequest(s)
		if err != nil {
			t.Fatalf("p8BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		// Read the desired composed resources from the RESPONSE. function-sdk-go
		// v0.7.1 has no response.GetDesiredComposedResources, so reuse the
		// request reader over a request carrying the response's desired state.
		resources, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources for spec %+v returned error: %v", s, err)
		}

		// The key set must be EXACTLY the two expected keys — no more, no fewer.
		if len(resources) != len(p8ExpectedKeys) {
			t.Fatalf("desired composed resources for spec %+v: expected exactly %d resources %v, got %d with keys %v",
				s, len(p8ExpectedKeys), p8ExpectedKeys, len(resources), p8Keys(resources))
		}

		for _, want := range p8ExpectedKeys {
			if _, ok := resources[want]; !ok {
				t.Fatalf("desired composed resources for spec %+v missing expected key %q; got keys %v",
					s, want, p8Keys(resources))
			}
		}
	})
}
