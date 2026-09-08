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

// pecr4Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pecr4Environments = []string{"dev", "staging", "prod"}

// pecr4Regions is the XRD region allow-list.
var pecr4Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pecr4TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pecr4TenantGen() *rapid.Generator[string] {
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

// pecr4Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// repository.enabled is fixed true so a Repository is always emitted; the other
// gated flag (table.enabled) and the S3 fields are varied to check the
// Repository namespace holds across the input space.
type pecr4Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
}

// pecr4GenSpec draws a valid TenantEnvironment spec with repository.enabled
// always true.
func pecr4GenSpec(t *rapid.T) pecr4Spec {
	return pecr4Spec{
		tenant:       pecr4TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(pecr4Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(pecr4Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
	}
}

// pecr4BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// repository.enabled: true.
func pecr4BuildRequest(s pecr4Spec) (*fnv1.RunFunctionRequest, error) {
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
				"enabled": true,
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

// Feature: tenant-environment-ecr, Property 4: The composed Repository lands in the namespace <tenant>-<env>
//
// For any valid TenantEnvironment spec with repository.enabled: true,
// RunFunction emits a "repository" desired resource whose metadata.namespace
// equals tenant + "-" + environment, the same namespace as the Bucket.
//
// Validates: Requirements 2.5
func TestPropertyECR4RepositoryNamespace(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pecr4GenSpec(t)

		req, err := pecr4BuildRequest(s)
		if err != nil {
			t.Fatalf("pecr4BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		// The "repository" and "bucket" entries must exist when enabled.
		repo, ok := dcds[keyRepository]
		if !ok {
			t.Fatalf("desired resources missing %q key with repository.enabled=true (spec=%+v)", keyRepository, s)
		}
		bucket, ok := dcds[keyBucket]
		if !ok {
			t.Fatalf("desired resources missing %q key (spec=%+v)", keyBucket, s)
		}

		// The Repository namespace must equal <tenant>-<env>.
		wantNamespace := s.tenant + "-" + s.environment
		gotNamespace := repo.Resource.GetNamespace()
		if gotNamespace != wantNamespace {
			t.Fatalf("desired resource %q namespace = %q; want %q (spec=%+v)",
				keyRepository, gotNamespace, wantNamespace, s)
		}

		// And it must match the Bucket's namespace.
		if bucketNamespace := bucket.Resource.GetNamespace(); gotNamespace != bucketNamespace {
			t.Fatalf("desired resource %q namespace = %q does not match bucket namespace %q (spec=%+v)",
				keyRepository, gotNamespace, bucketNamespace, s)
		}
	})
}
