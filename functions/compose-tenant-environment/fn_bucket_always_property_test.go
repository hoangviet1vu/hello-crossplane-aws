package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
)

// p1Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p1Environments = []string{"dev", "staging", "prod"}

// p1Regions is the XRD region allow-list. Any of these is valid for spec.region.
var p1Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// p1TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func p1TenantGen() *rapid.Generator[string] {
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

// p1Spec captures the drawn TenantEnvironment spec inputs for one iteration.
type p1Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repoEnabled  bool
}

// p1GenSpec draws a full valid TenantEnvironment spec, varying BOTH the
// table.enabled and repository.enabled flags (as well as tenant, environment,
// region, and bucket.versioning) so the property holds across the entire input
// space.
func p1GenSpec(t *rapid.T) p1Spec {
	return p1Spec{
		tenant:       p1TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(p1Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(p1Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repoEnabled:  rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// p1BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment (apiVersion platform.hello-crossplane.io/
// v1alpha1, kind TenantEnvironment) carrying the given spec.
func p1BuildRequest(s p1Spec) (*fnv1.RunFunctionRequest, error) {
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

// Feature: tenant-environment-s3, Property 1: The bucket is always composed
//
// For any valid TenantEnvironment spec — with table.enabled and
// repository.enabled taking any combination of true/false — RunFunction always
// returns a desired composed resources map that contains the key "bucket".
//
// Validates: Requirements 2.1, 2.2
func TestProperty1BucketAlwaysComposed(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := p1GenSpec(t)

		req, err := p1BuildRequest(s)
		if err != nil {
			t.Fatalf("p1BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		// Inspect the response's desired composed resources map directly. The
		// keys are the stable composition-resource names; "bucket" must always
		// be present regardless of the table/repository enabled flags.
		resources := rsp.GetDesired().GetResources()
		if _, ok := resources["bucket"]; !ok {
			keys := make([]string, 0, len(resources))
			for k := range resources {
				keys = append(keys, k)
			}
			t.Fatalf("desired composed resources for spec %+v missing key %q; got keys %v",
				s, "bucket", keys)
		}
	})
}
