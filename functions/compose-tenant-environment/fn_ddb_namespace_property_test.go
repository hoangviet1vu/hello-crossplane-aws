package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// pddb4Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb4Environments = []string{"dev", "staging", "prod"}

// pddb4Regions is the XRD region allow-list.
var pddb4Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb4TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb4TenantGen() *rapid.Generator[string] {
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

// pddb4Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// table.enabled is fixed true so a Table is always emitted; the other gated
// flag (repository.enabled) and the S3 fields are varied to check the Table
// namespace holds across the input space.
type pddb4Spec struct {
	tenant      string
	environment string
	region      string
	versioning  bool
	repoEnabled bool
}

// pddb4GenSpec draws a valid TenantEnvironment spec with table.enabled always
// true.
func pddb4GenSpec(t *rapid.T) pddb4Spec {
	return pddb4Spec{
		tenant:      pddb4TenantGen().Draw(t, "tenant"),
		environment: rapid.SampledFrom(pddb4Environments).Draw(t, "environment"),
		region:      rapid.SampledFrom(pddb4Regions).Draw(t, "region"),
		versioning:  rapid.Bool().Draw(t, "versioning"),
		repoEnabled: rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// pddb4BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// table.enabled: true.
func pddb4BuildRequest(s pddb4Spec) (*fnv1.RunFunctionRequest, error) {
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
				"enabled": true,
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

// Feature: tenant-environment-dynamodb, Property 4: The composed Table lands in the namespace <tenant>-<env>
//
// For any valid TenantEnvironment spec with table.enabled: true, RunFunction
// emits a "table" desired resource whose metadata.namespace equals
// tenant + "-" + environment, the same namespace as the Bucket.
//
// Validates: Requirements 2.5
func TestPropertyDDB4TableNamespace(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb4GenSpec(t)

		req, err := pddb4BuildRequest(s)
		if err != nil {
			t.Fatalf("pddb4BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		// The "table" and "bucket" entries must exist when table.enabled=true.
		table, ok := dcds[keyTable]
		if !ok {
			t.Fatalf("desired resources missing %q key with table.enabled=true (spec=%+v)", keyTable, s)
		}
		bucket, ok := dcds[keyBucket]
		if !ok {
			t.Fatalf("desired resources missing %q key (spec=%+v)", keyBucket, s)
		}

		// The Table namespace must equal <tenant>-<env>.
		wantNamespace := s.tenant + "-" + s.environment
		gotNamespace := table.Resource.GetNamespace()
		if gotNamespace != wantNamespace {
			t.Fatalf("desired resource %q namespace = %q; want %q (spec=%+v)",
				keyTable, gotNamespace, wantNamespace, s)
		}

		// And it must match the Bucket's namespace.
		if bucketNamespace := bucket.Resource.GetNamespace(); gotNamespace != bucketNamespace {
			t.Fatalf("desired resource %q namespace = %q does not match bucket namespace %q (spec=%+v)",
				keyTable, gotNamespace, bucketNamespace, s)
		}
	})
}
