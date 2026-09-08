package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/fieldpath"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// p7Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p7Environments = []string{"dev", "staging", "prod"}

// p7Regions is the XRD region allow-list.
var p7Regions = []string{"ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "us-east-1"}

// p7TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func p7TenantGenerator() *rapid.Generator[string] {
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

// p7BuildRequest assembles a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment with the given spec fields. The XR is encoded
// as a structpb.Struct on req.Observed.Composite.Resource, exactly as the SDK
// expects to read it back via request.GetObservedCompositeResource.
func p7BuildRequest(t *rapid.T, tenant, environment, region string, versioning, tableEnabled, repoEnabled bool) *fnv1.RunFunctionRequest {
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
		t.Fatalf("p7BuildRequest: cannot build structpb for XR: %v", err)
	}

	return &fnv1.RunFunctionRequest{
		Observed: &fnv1.State{
			Composite: &fnv1.Resource{
				Resource: s,
			},
		},
	}
}

// Feature: tenant-environment-s3, Property 7: Namespace consistency and versioning-to-bucket reference
//
// For any valid TenantEnvironment input, both the composed bucket and
// bucket-versioning resources are placed in the namespace <tenant>-<env>
// (metadata.namespace), and the BucketVersioning references the bucket by its
// external name <tenant>-<env>-bucket via spec.forProvider.bucket.
//
// Validates: Requirements 2.4, 4.6, 4.7
func TestProperty7NamespaceConsistencyAndVersioningReference(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		tenant := p7TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(p7Environments).Draw(t, "environment")
		region := rapid.SampledFrom(p7Regions).Draw(t, "region")
		versioning := rapid.Bool().Draw(t, "versioning")
		tableEnabled := rapid.Bool().Draw(t, "tableEnabled")
		repoEnabled := rapid.Bool().Draw(t, "repoEnabled")

		req := p7BuildRequest(t, tenant, environment, region, versioning, tableEnabled, repoEnabled)

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

		wantNamespace := tenant + "-" + environment
		wantBucketExtName := tenant + "-" + environment + "-bucket"

		// 1. Both the bucket and bucket-versioning resources must be present.
		bucket, ok := dcds[keyBucket]
		if !ok {
			t.Fatalf("desired resources missing %q key for tenant=%q, env=%q", keyBucket, tenant, environment)
		}
		versioningRes, ok := dcds[keyBucketVersioning]
		if !ok {
			t.Fatalf("desired resources missing %q key for tenant=%q, env=%q", keyBucketVersioning, tenant, environment)
		}

		// 2. Both resources are in namespace <tenant>-<env> (R2.4, R4.6).
		if got := bucket.Resource.GetNamespace(); got != wantNamespace {
			t.Fatalf("bucket metadata.namespace = %q; want %q (tenant=%q, env=%q)",
				got, wantNamespace, tenant, environment)
		}
		if got := versioningRes.Resource.GetNamespace(); got != wantNamespace {
			t.Fatalf("bucket-versioning metadata.namespace = %q; want %q (tenant=%q, env=%q)",
				got, wantNamespace, tenant, environment)
		}

		// 3. The BucketVersioning references the bucket by its external name
		// <tenant>-<env>-bucket via spec.forProvider.bucket (R4.7).
		paved := fieldpath.Pave(versioningRes.Resource.UnstructuredContent())
		gotRef, err := paved.GetString("spec.forProvider.bucket")
		if err != nil {
			t.Fatalf("cannot read spec.forProvider.bucket from bucket-versioning (tenant=%q, env=%q): %v",
				tenant, environment, err)
		}
		if gotRef != wantBucketExtName {
			t.Fatalf("bucket-versioning spec.forProvider.bucket = %q; want %q (tenant=%q, env=%q)",
				gotRef, wantBucketExtName, tenant, environment)
		}
	})
}
