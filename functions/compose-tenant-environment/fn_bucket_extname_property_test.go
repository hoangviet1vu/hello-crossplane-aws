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

// p2Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p2Environments = []string{"dev", "staging", "prod"}

// p2Regions is the XRD region allow-list.
var p2Regions = []string{"ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "us-east-1"}

// p2TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between,
// for a total length of 3–22 characters.
func p2TenantGenerator() *rapid.Generator[string] {
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

// p2BuildRequest assembles a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment with the given spec fields. The XR is encoded
// as a structpb.Struct on req.Observed.Composite.Resource, exactly as the SDK
// expects to read it back via request.GetObservedCompositeResource.
func p2BuildRequest(t *rapid.T, tenant, environment, region string, versioning, tableEnabled, repoEnabled bool) *fnv1.RunFunctionRequest {
	t.Helper()

	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name":      tenant + "-" + environment,
			"namespace": tenant + "-" + environment,
		},
		"spec": map[string]any{
			"tenant":      tenant,
			"environment": environment,
			"region":      region,
			"bucket": map[string]any{
				"versioning": versioning,
			},
			"table": map[string]any{
				"enabled": tableEnabled,
			},
			"repository": map[string]any{
				"enabled": repoEnabled,
			},
		},
	}

	s, err := structpb.NewStruct(xr)
	if err != nil {
		t.Fatalf("p2BuildRequest: cannot build structpb for XR: %v", err)
	}

	return &fnv1.RunFunctionRequest{
		Observed: &fnv1.State{
			Composite: &fnv1.Resource{
				Resource: s,
			},
		},
	}
}

// Feature: tenant-environment-s3, Property 2: Bucket external name format and no metadata name derivation
//
// For any valid tenant and environment, the composed bucket's
// crossplane.io/external-name annotation equals the literal <tenant>-<env>-bucket,
// and the bucket's metadata.name is not set to that value nor to any other value
// derived from tenant/environment (namely <tenant>-<env>, tenant, or environment).
// metadata.name must be empty/unset.
//
// Validates: Requirements 2.6, 3.1, 3.3, 3.4
func TestProperty2BucketExternalNameAndNoMetadataName(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		tenant := p2TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(p2Environments).Draw(t, "environment")
		region := rapid.SampledFrom(p2Regions).Draw(t, "region")
		versioning := rapid.Bool().Draw(t, "versioning")
		tableEnabled := rapid.Bool().Draw(t, "tableEnabled")
		repoEnabled := rapid.Bool().Draw(t, "repoEnabled")

		req := p2BuildRequest(t, tenant, environment, region, versioning, tableEnabled, repoEnabled)

		rsp, err := fn.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(tenant=%q, env=%q) returned transport error: %v", tenant, environment, err)
		}

		// The desired composed resources live on the response's Desired state,
		// which shares its shape with a request. Wrap it in a request so we can
		// read the composed resources back through the SDK's typed accessor.
		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(tenant=%q, env=%q) error: %v", tenant, environment, err)
		}

		bucket, ok := dcds[keyBucket]
		if !ok {
			t.Fatalf("desired resources missing %q key for tenant=%q, env=%q", keyBucket, tenant, environment)
		}

		// 1. The external-name annotation equals the literal <tenant>-<env>-bucket.
		want := tenant + "-" + environment + "-bucket"
		gotExtName := bucket.Resource.GetAnnotations()[externalNameAnnotation]
		if gotExtName != want {
			t.Fatalf("bucket %s annotation = %q; want %q (tenant=%q, env=%q)",
				externalNameAnnotation, gotExtName, want, tenant, environment)
		}

		// 2. metadata.name must be empty/unset, and in particular must not be
		// any tenant/environment-derived value.
		gotName := bucket.Resource.GetName()
		if gotName != "" {
			t.Fatalf("bucket metadata.name = %q; want empty/unset (tenant=%q, env=%q)",
				gotName, tenant, environment)
		}

		// Defense in depth: even if the emptiness check were relaxed, the name
		// must never be one of the tenant/env-derived values.
		derived := map[string]struct{}{
			want:                       {}, // <tenant>-<env>-bucket
			tenant + "-" + environment: {}, // <tenant>-<env>
			tenant:                     {},
			environment:                {},
		}
		if _, isDerived := derived[gotName]; isDerived {
			t.Fatalf("bucket metadata.name = %q is a tenant/env-derived value; must not be derived (tenant=%q, env=%q)",
				gotName, tenant, environment)
		}
	})
}
