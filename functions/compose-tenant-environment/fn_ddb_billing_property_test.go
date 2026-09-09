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

// pddb6Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb6Environments = []string{"dev", "staging", "prod"}

// pddb6Regions is the XRD region allow-list.
var pddb6Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb6BillingModes is the XRD billingMode enum: the two valid values the
// generator draws from when spec.table.billingMode is present.
var pddb6BillingModes = []string{defaultBillingMode, billingModeProvisioned}

// pddb6TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb6TenantGen() *rapid.Generator[string] {
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

// pddb6Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// table.enabled is fixed true so a Table is always emitted; billingModePresent
// is varied so both the explicit-billingMode and default ("PAY_PER_REQUEST")
// branches are exercised, and billingMode draws from both enum values.
type pddb6Spec struct {
	tenant             string
	environment        string
	region             string
	versioning         bool
	billingModePresent bool
	billingMode        string
}

// wantBillingMode returns the billing mode the function should use: the drawn
// value when present, otherwise the schema default "PAY_PER_REQUEST".
func (s pddb6Spec) wantBillingMode() string {
	if s.billingModePresent {
		return s.billingMode
	}
	return defaultBillingMode
}

// pddb6GenSpec draws a valid TenantEnvironment spec with table.enabled always
// true, varying whether spec.table.billingMode is present and, when present,
// which of the two enum values it takes.
func pddb6GenSpec(t *rapid.T) pddb6Spec {
	return pddb6Spec{
		tenant:             pddb6TenantGen().Draw(t, "tenant"),
		environment:        rapid.SampledFrom(pddb6Environments).Draw(t, "environment"),
		region:             rapid.SampledFrom(pddb6Regions).Draw(t, "region"),
		versioning:         rapid.Bool().Draw(t, "versioning"),
		billingModePresent: rapid.Bool().Draw(t, "billingModePresent"),
		billingMode:        rapid.SampledFrom(pddb6BillingModes).Draw(t, "billingMode"),
	}
}

// pddb6BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// table.enabled: true. spec.table.billingMode is included only when present so
// the absent case exercises the schema default.
func pddb6BuildRequest(s pddb6Spec) (*fnv1.RunFunctionRequest, error) {
	table := map[string]any{
		"enabled": true,
	}
	if s.billingModePresent {
		table["billingMode"] = s.billingMode
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

// pddb6ForProvider reads spec.forProvider off a desired composed resource's
// unstructured content, returning the map and whether it was present.
func pddb6ForProvider(dcd *resource.DesiredComposed) (map[string]any, bool) {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return nil, false
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return nil, false
	}
	return forProvider, true
}

// pddb6ReadCapacity reads a capacity field (readCapacity / writeCapacity) off a
// forProvider map. The generated capacities are *float32; in the unstructured
// desired resource they appear as JSON numbers. Read them robustly across the
// numeric types JSON round-tripping can produce.
func pddb6ReadCapacity(forProvider map[string]any, field string) (float64, bool) {
	raw, ok := forProvider[field]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

// Feature: tenant-environment-dynamodb, Property 6: Billing mode and capacity are consistent
//
// For any valid TenantEnvironment spec with table.enabled: true, the composed
// "table" resource has spec.forProvider.billingMode equal to
// spec.table.billingMode (defaulting to "PAY_PER_REQUEST" when absent); where
// the mode is PAY_PER_REQUEST no readCapacity/writeCapacity is set, and where
// the mode is PROVISIONED both are set to 1.
//
// Validates: Requirements 4.4, 4.5, 4.6, 4.7
func TestPropertyDDB6BillingModeAndCapacity(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb6GenSpec(t)

		req, err := pddb6BuildRequest(s)
		if err != nil {
			t.Fatalf("pddb6BuildRequest(%+v) returned error: %v", s, err)
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

		forProvider, ok := pddb6ForProvider(table)
		if !ok {
			t.Fatalf("table spec.forProvider is unset (spec=%+v)", s)
		}

		want := s.wantBillingMode()

		// 1. spec.forProvider.billingMode equals the drawn mode (or the default).
		gotMode, ok := forProvider["billingMode"].(string)
		if !ok {
			t.Fatalf("table spec.forProvider.billingMode is unset or non-string; want %q (spec=%+v)", want, s)
		}
		if gotMode != want {
			t.Fatalf("table spec.forProvider.billingMode = %q; want %q (spec=%+v)", gotMode, want, s)
		}

		// 2. Capacity gating: unset for PAY_PER_REQUEST, both == 1 for PROVISIONED.
		readCap, hasRead := pddb6ReadCapacity(forProvider, "readCapacity")
		writeCap, hasWrite := pddb6ReadCapacity(forProvider, "writeCapacity")

		switch want {
		case billingModeProvisioned:
			if !hasRead {
				t.Fatalf("table spec.forProvider.readCapacity is unset; want 1 for PROVISIONED (spec=%+v)", s)
			}
			if readCap != 1 {
				t.Fatalf("table spec.forProvider.readCapacity = %v; want 1 for PROVISIONED (spec=%+v)", readCap, s)
			}
			if !hasWrite {
				t.Fatalf("table spec.forProvider.writeCapacity is unset; want 1 for PROVISIONED (spec=%+v)", s)
			}
			if writeCap != 1 {
				t.Fatalf("table spec.forProvider.writeCapacity = %v; want 1 for PROVISIONED (spec=%+v)", writeCap, s)
			}
		default: // PAY_PER_REQUEST
			if hasRead {
				t.Fatalf("table spec.forProvider.readCapacity = %v; want unset for PAY_PER_REQUEST (spec=%+v)", readCap, s)
			}
			if hasWrite {
				t.Fatalf("table spec.forProvider.writeCapacity = %v; want unset for PAY_PER_REQUEST (spec=%+v)", writeCap, s)
			}
		}
	})
}
