package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
)

// pddb3Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb3Environments = []string{"dev", "staging", "prod"}

// pddb3Regions is the XRD region allow-list.
var pddb3Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb3TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb3TenantGen() *rapid.Generator[string] {
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

// pddb3Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// table.enabled is fixed true so a Table is always emitted; the other gated flag
// (repository.enabled) and the S3 field (versioning) are varied so the
// external-name / region / no-metadata-name invariant is checked across the
// input space.
type pddb3Spec struct {
	tenant      string
	environment string
	region      string
	versioning  bool
	repoEnabled bool
}

// pddb3GenSpec draws a valid TenantEnvironment spec with table.enabled always
// true.
func pddb3GenSpec(t *rapid.T) pddb3Spec {
	return pddb3Spec{
		tenant:      pddb3TenantGen().Draw(t, "tenant"),
		environment: rapid.SampledFrom(pddb3Environments).Draw(t, "environment"),
		region:      rapid.SampledFrom(pddb3Regions).Draw(t, "region"),
		versioning:  rapid.Bool().Draw(t, "versioning"),
		repoEnabled: rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// pddb3BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// table.enabled: true.
func pddb3BuildRequest(s pddb3Spec) (*fnv1.RunFunctionRequest, error) {
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

// pddb3ReadRegion reads spec.forProvider.region off a desired composed
// resource's unstructured content. It returns the region string and whether the
// field was present.
func pddb3ReadRegion(dcd *resource.DesiredComposed) (string, bool) {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return "", false
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return "", false
	}
	region, ok := forProvider["region"].(string)
	if !ok {
		return "", false
	}
	return region, true
}

// Feature: tenant-environment-dynamodb, Property 3: Table external-name equals <tenant>-<env>-dtbl
//
// For any valid TenantEnvironment spec with table.enabled: true, the composed
// "table" resource carries a crossplane.io/external-name annotation exactly
// equal to <tenant>-<env>-dtbl, its spec.forProvider.region equals the drawn
// spec.region, and its metadata.name is not set to the external name nor to any
// other tenant/environment-derived value.
//
// Validates: Requirements 2.6, 2.8, 3.1, 3.3, 3.4
func TestPropertyDDB3TableExternalNameRegionAndNoMetadataName(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb3GenSpec(t)

		req, err := pddb3BuildRequest(s)
		if err != nil {
			t.Fatalf("pddb3BuildRequest(%+v) returned error: %v", s, err)
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
		if !ok {
			t.Fatalf("desired resources missing %q key with table.enabled=true (spec=%+v)", keyTable, s)
		}

		// 1. The external-name annotation equals the literal <tenant>-<env>-dtbl.
		want := s.tenant + "-" + s.environment + "-dtbl"
		gotExtName := table.Resource.GetAnnotations()[externalNameAnnotation]
		if gotExtName != want {
			t.Fatalf("table %s annotation = %q; want %q (spec=%+v)",
				externalNameAnnotation, gotExtName, want, s)
		}

		// 2. spec.forProvider.region equals the drawn region.
		gotRegion, hasRegion := pddb3ReadRegion(table)
		if !hasRegion {
			t.Fatalf("table spec.forProvider.region is unset; want %q (spec=%+v)", s.region, s)
		}
		if gotRegion != s.region {
			t.Fatalf("table spec.forProvider.region = %q; want %q (spec=%+v)",
				gotRegion, s.region, s)
		}

		// 3. metadata.name must be empty/unset, and in particular must not be
		// any tenant/environment-derived value.
		gotName := table.Resource.GetName()
		if gotName != "" {
			t.Fatalf("table metadata.name = %q; want empty/unset (spec=%+v)", gotName, s)
		}

		// Defense in depth: even if the emptiness check were relaxed, the name
		// must never be the external name nor any other tenant/env-derived value.
		derived := map[string]struct{}{
			want:                           {}, // <tenant>-<env>-dtbl
			s.tenant + "-" + s.environment: {}, // <tenant>-<env>
			s.tenant:                       {},
			s.environment:                  {},
		}
		if _, isDerived := derived[gotName]; isDerived {
			t.Fatalf("table metadata.name = %q is a tenant/env-derived value; must not be derived (spec=%+v)",
				gotName, s)
		}
	})
}
