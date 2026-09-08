package main

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// p6Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p6Environments = []string{"dev", "staging", "prod"}

// p6Regions is the XRD region allow-list.
var p6Regions = []string{"ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "us-east-1"}

// p6VersioningState models the three possibilities for spec.bucket.versioning:
// explicitly true, explicitly false, or entirely absent (the field omitted).
type p6VersioningState int

const (
	p6VersioningTrue p6VersioningState = iota
	p6VersioningFalse
	p6VersioningAbsent
)

// p6TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between,
// for a total length of 3–22 characters.
func p6TenantGenerator() *rapid.Generator[string] {
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

// p6BuildRequest assembles a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment with the given spec fields. The versioning
// field is included only when state is true or false; when state is absent the
// spec.bucket map is omitted entirely, exercising the schema-default path.
func p6BuildRequest(t *rapid.T, tenant, environment, region string, state p6VersioningState) *fnv1.RunFunctionRequest {
	t.Helper()

	spec := map[string]any{
		"tenant":      tenant,
		"environment": environment,
		"region":      region,
		"table": map[string]any{
			"enabled": false,
		},
		"repository": map[string]any{
			"enabled": false,
		},
	}

	switch state {
	case p6VersioningTrue:
		spec["bucket"] = map[string]any{"versioning": true}
	case p6VersioningFalse:
		spec["bucket"] = map[string]any{"versioning": false}
	case p6VersioningAbsent:
		// Deliberately omit spec.bucket entirely so the function resolves the
		// schema default (true → Enabled).
	}

	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name":      tenant + "-" + environment,
			"namespace": tenant + "-" + environment,
		},
		"spec": spec,
	}

	s, err := structpb.NewStruct(xr)
	if err != nil {
		t.Fatalf("p6BuildRequest: cannot build structpb for XR: %v", err)
	}

	return &fnv1.RunFunctionRequest{
		Observed: &fnv1.State{
			Composite: &fnv1.Resource{
				Resource: s,
			},
		},
	}
}

// Feature: tenant-environment-s3, Property 6: Versioning status maps from the spec flag
//
// For any valid tenant, environment, and region, and for spec.bucket.versioning
// being true, false, or absent, the composed bucket-versioning resource's
// spec.forProvider.versioningConfiguration.status is exactly "Enabled" when the
// flag is true or absent, exactly "Suspended" when the flag is false, and never
// any other value.
//
// Validates: Requirements 4.3, 4.4, 4.5
func TestProperty6VersioningStatusMapsFromSpecFlag(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		tenant := p6TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(p6Environments).Draw(t, "environment")
		region := rapid.SampledFrom(p6Regions).Draw(t, "region")
		state := rapid.SampledFrom([]p6VersioningState{
			p6VersioningTrue, p6VersioningFalse, p6VersioningAbsent,
		}).Draw(t, "versioningState")

		req := p6BuildRequest(t, tenant, environment, region, state)

		rsp, err := fn.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(tenant=%q, env=%q, state=%d) returned transport error: %v",
				tenant, environment, state, err)
		}

		// The desired composed resources live on the response's Desired state,
		// which shares its shape with a request. Wrap it in a request so we can
		// read the composed resources back through the SDK's typed accessor.
		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(tenant=%q, env=%q, state=%d) error: %v",
				tenant, environment, state, err)
		}

		bv, ok := dcds[keyBucketVersioning]
		if !ok {
			t.Fatalf("desired resources missing %q key for tenant=%q, env=%q, state=%d",
				keyBucketVersioning, tenant, environment, state)
		}

		// Read spec.forProvider.versioningConfiguration.status via a nested
		// lookup on the composed resource's unstructured content.
		gotStatus, err := p6NestedString(bv.Resource.UnstructuredContent(),
			"spec", "forProvider", "versioningConfiguration", "status")
		if err != nil {
			t.Fatalf("cannot read versioning status (tenant=%q, env=%q, state=%d): %v",
				tenant, environment, state, err)
		}

		want := "Enabled"
		if state == p6VersioningFalse {
			want = "Suspended"
		}

		if gotStatus != want {
			t.Fatalf("versioning status = %q; want %q (tenant=%q, env=%q, state=%d)",
				gotStatus, want, tenant, environment, state)
		}

		// The status must never be any value other than Enabled or Suspended.
		if gotStatus != "Enabled" && gotStatus != "Suspended" {
			t.Fatalf("versioning status = %q; want one of Enabled/Suspended (tenant=%q, env=%q, state=%d)",
				gotStatus, tenant, environment, state)
		}
	})
}

// p6NestedString walks a chain of map keys on an unstructured object and returns
// the string value at the leaf. It errors if any intermediate key is missing,
// not a map, or if the leaf is missing or not a string.
func p6NestedString(obj map[string]any, keys ...string) (string, error) {
	cur := obj
	for i, k := range keys {
		v, ok := cur[k]
		if !ok {
			return "", fmt.Errorf("key %q not found at depth %d", k, i)
		}
		if i == len(keys)-1 {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("value at key %q is %T, not string", k, v)
			}
			return s, nil
		}
		m, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("value at key %q is %T, not map[string]any", k, v)
		}
		cur = m
	}
	return "", fmt.Errorf("empty key path")
}
