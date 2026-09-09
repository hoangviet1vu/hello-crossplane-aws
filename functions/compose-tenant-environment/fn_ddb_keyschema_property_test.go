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

// pddb5Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb5Environments = []string{"dev", "staging", "prod"}

// pddb5Regions is the XRD region allow-list.
var pddb5Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb5TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb5TenantGen() *rapid.Generator[string] {
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

// pddb5HashKeyGen produces a non-empty lowercase-alphanumeric identifier for
// spec.table.hashKey (1–30 characters).
func pddb5HashKeyGen() *rapid.Generator[string] {
	ch := rapid.SampledFrom([]byte("abcdefghijklmnopqrstuvwxyz0123456789"))
	return rapid.Custom(func(t *rapid.T) string {
		b := rapid.SliceOfN(ch, 1, 30).Draw(t, "hashKey")
		return string(b)
	})
}

// pddb5Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// table.enabled is fixed true so a Table is always emitted; hashKeyPresent is
// varied so both the explicit-hashKey and default ("id") branches are exercised.
type pddb5Spec struct {
	tenant         string
	environment    string
	region         string
	versioning     bool
	hashKeyPresent bool
	hashKey        string
}

// wantHashKey returns the hash key the function should use: the drawn value when
// present, otherwise the schema default "id".
func (s pddb5Spec) wantHashKey() string {
	if s.hashKeyPresent {
		return s.hashKey
	}
	return defaultHashKey
}

// pddb5GenSpec draws a valid TenantEnvironment spec with table.enabled always
// true, varying whether spec.table.hashKey is present.
func pddb5GenSpec(t *rapid.T) pddb5Spec {
	return pddb5Spec{
		tenant:         pddb5TenantGen().Draw(t, "tenant"),
		environment:    rapid.SampledFrom(pddb5Environments).Draw(t, "environment"),
		region:         rapid.SampledFrom(pddb5Regions).Draw(t, "region"),
		versioning:     rapid.Bool().Draw(t, "versioning"),
		hashKeyPresent: rapid.Bool().Draw(t, "hashKeyPresent"),
		hashKey:        pddb5HashKeyGen().Draw(t, "hashKey"),
	}
}

// pddb5BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// table.enabled: true. spec.table.hashKey is included only when present so the
// absent case exercises the schema default.
func pddb5BuildRequest(s pddb5Spec) (*fnv1.RunFunctionRequest, error) {
	table := map[string]any{
		"enabled": true,
	}
	if s.hashKeyPresent {
		table["hashKey"] = s.hashKey
	}

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
			"table": table,
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

// pddb5ReadHashKey reads spec.forProvider.hashKey off a desired composed
// resource's unstructured content. It returns the hash key string and whether
// the field was present.
func pddb5ReadHashKey(dcd *resource.DesiredComposed) (string, bool) {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return "", false
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return "", false
	}
	hashKey, ok := forProvider["hashKey"].(string)
	if !ok {
		return "", false
	}
	return hashKey, true
}

// pddb5ReadAttributes reads spec.forProvider.attribute off a desired composed
// resource's unstructured content, returning the list of attribute maps and
// whether the field was present.
func pddb5ReadAttributes(dcd *resource.DesiredComposed) ([]any, bool) {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return nil, false
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return nil, false
	}
	attrs, ok := forProvider["attribute"].([]any)
	if !ok {
		return nil, false
	}
	return attrs, true
}

// Feature: tenant-environment-dynamodb, Property 5: Key schema mirrors the spec
//
// For any valid TenantEnvironment spec with table.enabled: true, the composed
// "table" resource has spec.forProvider.hashKey equal to spec.table.hashKey
// (defaulting to "id" when absent), and declares exactly one
// spec.forProvider.attribute item whose name equals that hash key and whose type
// is "S".
//
// Validates: Requirements 4.1, 4.2, 4.3
func TestPropertyDDB5KeySchemaMirrorsSpec(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb5GenSpec(t)

		req, err := pddb5BuildRequest(s)
		if err != nil {
			t.Fatalf("pddb5BuildRequest(%+v) returned error: %v", s, err)
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

		want := s.wantHashKey()

		// 1. spec.forProvider.hashKey equals the drawn hash key (or the default).
		gotHashKey, hasHashKey := pddb5ReadHashKey(table)
		if !hasHashKey {
			t.Fatalf("table spec.forProvider.hashKey is unset; want %q (spec=%+v)", want, s)
		}
		if gotHashKey != want {
			t.Fatalf("table spec.forProvider.hashKey = %q; want %q (spec=%+v)", gotHashKey, want, s)
		}

		// 2. Exactly one attribute item.
		attrs, hasAttrs := pddb5ReadAttributes(table)
		if !hasAttrs {
			t.Fatalf("table spec.forProvider.attribute is unset; want exactly one item (spec=%+v)", s)
		}
		if len(attrs) != 1 {
			t.Fatalf("table spec.forProvider.attribute has %d items; want exactly 1 (spec=%+v)", len(attrs), s)
		}

		// 3. attribute[0].name equals the hash key and attribute[0].type is "S".
		attr, ok := attrs[0].(map[string]any)
		if !ok {
			t.Fatalf("table spec.forProvider.attribute[0] is not an object: %T (spec=%+v)", attrs[0], s)
		}

		gotName, ok := attr["name"].(string)
		if !ok {
			t.Fatalf("table attribute[0].name is unset or non-string; want %q (spec=%+v)", want, s)
		}
		if gotName != want {
			t.Fatalf("table attribute[0].name = %q; want %q (spec=%+v)", gotName, want, s)
		}

		gotType, ok := attr["type"].(string)
		if !ok {
			t.Fatalf("table attribute[0].type is unset or non-string; want %q (spec=%+v)", "S", s)
		}
		if gotType != "S" {
			t.Fatalf("table attribute[0].type = %q; want %q (spec=%+v)", gotType, "S", s)
		}
	})
}
