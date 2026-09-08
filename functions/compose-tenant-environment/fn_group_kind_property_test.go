package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// p5NamespacedS3APIVersion is the ONLY apiVersion any composed S3 resource may
// carry: the namespaced (.m.) API group. The legacy cluster-scoped group
// s3.aws.upbound.io is wrong for a namespaced XR and must never appear.
const p5NamespacedS3APIVersion = "s3.aws.m.upbound.io/v1beta1"

// p5LegacyS3Group is the legacy cluster-scoped group prefix that must never
// appear on any composed resource.
const p5LegacyS3Group = "s3.aws.upbound.io"

// p5Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p5Environments = []string{"dev", "staging", "prod"}

// p5Regions is the XRD region allow-list.
var p5Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// p5TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func p5TenantGen() *rapid.Generator[string] {
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

// p5Spec captures the drawn TenantEnvironment spec inputs for one iteration.
type p5Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repoEnabled  bool
}

// p5GenSpec draws a full valid TenantEnvironment spec, varying every field
// (including the table/repository enabled flags) so the group/Kind property is
// checked across the entire input space.
func p5GenSpec(t *rapid.T) p5Spec {
	return p5Spec{
		tenant:       p5TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(p5Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(p5Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repoEnabled:  rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// p5BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment (apiVersion platform.hello-crossplane.io/
// v1alpha1, kind TenantEnvironment) carrying the given spec.
func p5BuildRequest(s p5Spec) (*fnv1.RunFunctionRequest, error) {
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

// Feature: tenant-environment-s3, Property 5: Every composed resource uses the namespaced S3 group and correct Kind
//
// For any valid TenantEnvironment spec, RunFunction emits only namespaced S3
// resources: EVERY desired composed resource carries apiVersion
// s3.aws.m.upbound.io/v1beta1 (never the legacy cluster-scoped s3.aws.upbound.io
// group), the "bucket" entry has Kind Bucket, and the "bucket-versioning" entry
// has Kind BucketVersioning.
//
// Validates: Requirements 2.3, 4.2, 6.4
func TestProperty5NamespacedGroupAndKind(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := p5GenSpec(t)

		req, err := p5BuildRequest(s)
		if err != nil {
			t.Fatalf("p5BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		// The desired composed resources live on the response's Desired state,
		// which shares its shape with a request. Wrap it in a request so we can
		// read the composed resources back through the SDK's typed accessor.
		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		// Every emitted resource must use the namespaced S3 apiVersion. The
		// legacy cluster-scoped group must never appear on any resource.
		for name, dcd := range dcds {
			gotAPIVersion := dcd.Resource.GetAPIVersion()
			if gotAPIVersion != p5NamespacedS3APIVersion {
				t.Fatalf("desired resource %q apiVersion = %q; want %q (spec=%+v)",
					name, gotAPIVersion, p5NamespacedS3APIVersion, s)
			}
			if strings.HasPrefix(gotAPIVersion, p5LegacyS3Group+"/") || gotAPIVersion == p5LegacyS3Group {
				t.Fatalf("desired resource %q uses legacy cluster-scoped group %q; want namespaced group (spec=%+v)",
					name, gotAPIVersion, s)
			}
		}

		// The "bucket" entry must exist and have Kind Bucket.
		bucket, ok := dcds[keyBucket]
		if !ok {
			t.Fatalf("desired resources missing %q key (spec=%+v)", keyBucket, s)
		}
		if got := bucket.Resource.GetKind(); got != "Bucket" {
			t.Fatalf("desired resource %q Kind = %q; want %q (spec=%+v)", keyBucket, got, "Bucket", s)
		}

		// The "bucket-versioning" entry must exist and have Kind BucketVersioning.
		versioning, ok := dcds[keyBucketVersioning]
		if !ok {
			t.Fatalf("desired resources missing %q key (spec=%+v)", keyBucketVersioning, s)
		}
		if got := versioning.Resource.GetKind(); got != "BucketVersioning" {
			t.Fatalf("desired resource %q Kind = %q; want %q (spec=%+v)", keyBucketVersioning, got, "BucketVersioning", s)
		}
	})
}
