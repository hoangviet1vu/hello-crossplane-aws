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

// p11Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod). Used only for the WELL-FORMED half of a spec whose
// OTHER required field is malformed.
var p11Environments = []string{"dev", "staging", "prod"}

// p11BadStrings is a set of malformed values for a required string field:
// empty, and various whitespace-only strings. BuildNames trims these to "" and
// rejects them, driving the function down the fatal path.
var p11BadStrings = []string{"", " ", "   ", "\t", "\n", " \t\n "}

// p11GoodTenant produces a well-formed tenant matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$, used when the malformed field is the
// ENVIRONMENT rather than the tenant.
func p11GoodTenant() *rapid.Generator[string] {
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

// p11 kind enumerates the shapes of malformed input the generator can produce.
// Each shape omits or malforms a required spec field so that RunFunction takes
// a fatal path (unreadable XR, GetString error, or BuildNames rejection).
const (
	p11MissingSpec        = "missing-spec"        // spec key entirely absent
	p11MissingTenant      = "missing-tenant"      // spec.tenant key absent
	p11EmptyTenant        = "empty-tenant"        // spec.tenant == "" or whitespace
	p11MissingEnvironment = "missing-environment" // spec.environment key absent
	p11EmptyEnvironment   = "empty-environment"   // spec.environment == "" or whitespace
)

// p11Case captures the malformed XR shape drawn for one iteration.
type p11Case struct {
	kind        string
	tenant      string // used when kind involves a present-but-malformed tenant
	environment string // used when kind involves a present-but-malformed environment
}

// p11GenCase draws one malformed-XR shape. It covers: spec entirely absent,
// missing tenant key, empty/whitespace tenant, missing environment key, and
// empty/whitespace environment. For shapes that malform one field, the OTHER
// required field is well-formed so the failure is isolated to the intended
// field.
func p11GenCase(t *rapid.T) p11Case {
	kind := rapid.SampledFrom([]string{
		p11MissingSpec,
		p11MissingTenant,
		p11EmptyTenant,
		p11MissingEnvironment,
		p11EmptyEnvironment,
	}).Draw(t, "kind")

	c := p11Case{kind: kind}
	switch kind {
	case p11EmptyTenant:
		// Malformed tenant; well-formed environment.
		c.tenant = rapid.SampledFrom(p11BadStrings).Draw(t, "badTenant")
		c.environment = rapid.SampledFrom(p11Environments).Draw(t, "goodEnv")
	case p11MissingTenant:
		// Tenant key omitted; well-formed environment.
		c.environment = rapid.SampledFrom(p11Environments).Draw(t, "goodEnv")
	case p11EmptyEnvironment:
		// Well-formed tenant; malformed environment.
		c.tenant = p11GoodTenant().Draw(t, "goodTenant")
		c.environment = rapid.SampledFrom(p11BadStrings).Draw(t, "badEnv")
	case p11MissingEnvironment:
		// Well-formed tenant; environment key omitted.
		c.tenant = p11GoodTenant().Draw(t, "goodTenant")
	case p11MissingSpec:
		// Nothing else needed; the whole spec is omitted.
	}
	return c
}

// p11BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment with apiVersion/kind present (so
// GetObservedCompositeResource can read it) but a spec that is malformed per
// the drawn case. Fields are OMITTED by not including the key — structpb.New
// Struct rejects nil values, so absence is modelled by leaving the key out.
func p11BuildRequest(c p11Case) (*fnv1.RunFunctionRequest, error) {
	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name": "malformed",
		},
	}

	// Build the spec only when the case is not "spec entirely absent". Keys are
	// added conditionally so an omitted field is truly absent (not present-nil).
	if c.kind != p11MissingSpec {
		spec := map[string]any{}
		switch c.kind {
		case p11MissingTenant:
			// tenant omitted; environment present and valid.
			spec["environment"] = c.environment
		case p11EmptyTenant:
			// tenant present but empty/whitespace; environment present and valid.
			spec["tenant"] = c.tenant
			spec["environment"] = c.environment
		case p11MissingEnvironment:
			// environment omitted; tenant present and valid.
			spec["tenant"] = c.tenant
		case p11EmptyEnvironment:
			// environment present but empty/whitespace; tenant present and valid.
			spec["tenant"] = c.tenant
			spec["environment"] = c.environment
		}
		xr["spec"] = spec
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

// p11HasFatal reports whether the response results contain a fatal entry.
func p11HasFatal(rsp *fnv1.RunFunctionResponse) bool {
	for _, r := range rsp.GetResults() {
		if r.GetSeverity() == fnv1.Severity_SEVERITY_FATAL {
			return true
		}
	}
	return false
}

// Feature: tenant-environment-s3, Property 11: Invalid spec yields a fatal result and no resources
//
// For any TenantEnvironment XR whose required spec fields are missing or
// malformed — spec entirely absent, tenant key missing, tenant empty/whitespace,
// environment key missing, or environment empty/whitespace — RunFunction returns
// a response that (a) carries a FATAL result and (b) contains ZERO desired
// composed resources.
//
// Validates: Requirements 7.1
func TestProperty11InvalidSpecFatalNoResources(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		c := p11GenCase(t)

		req, err := p11BuildRequest(c)
		if err != nil {
			t.Fatalf("p11BuildRequest(%+v) returned error: %v", c, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", c, err)
		}

		// (a) The response must carry a fatal result.
		if !p11HasFatal(rsp) {
			t.Fatalf("case %+v: expected a FATAL result, got results %v", c, rsp.GetResults())
		}

		// (b) The response must contain zero desired composed resources. Read
		// them from the RESPONSE by reusing the request reader over a request
		// carrying the response's desired state.
		resources, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("case %+v: GetDesiredComposedResources returned error: %v", c, err)
		}
		if len(resources) != 0 {
			t.Fatalf("case %+v: expected zero desired composed resources, got %d", c, len(resources))
		}
	})
}
