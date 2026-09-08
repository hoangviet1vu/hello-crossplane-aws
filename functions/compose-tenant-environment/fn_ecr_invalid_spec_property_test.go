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

// pecr7Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod). Used only for the WELL-FORMED half of a spec whose
// OTHER required field is malformed.
var pecr7Environments = []string{"dev", "staging", "prod"}

// pecr7BadStrings is a set of malformed values for a required string field:
// empty, and various whitespace-only strings. BuildNames trims these to "" and
// rejects them, driving the function down the fatal path.
var pecr7BadStrings = []string{"", " ", "   ", "\t", "\n", " \t\n "}

// pecr7GoodTenant produces a well-formed tenant matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$, used when the malformed field is the
// ENVIRONMENT rather than the tenant.
func pecr7GoodTenant() *rapid.Generator[string] {
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

// pecr7 kind enumerates the shapes of malformed input the generator can
// produce. Each shape omits or malforms a required spec field, or makes the
// observed composite unreadable, so RunFunction takes a fatal path (unreadable
// XR, GetString error, or BuildNames rejection).
const (
	pecr7MissingSpec        = "missing-spec"        // spec key entirely absent
	pecr7MissingTenant      = "missing-tenant"      // spec.tenant key absent
	pecr7EmptyTenant        = "empty-tenant"        // spec.tenant == "" or whitespace
	pecr7MissingEnvironment = "missing-environment" // spec.environment key absent
	pecr7EmptyEnvironment   = "empty-environment"   // spec.environment == "" or whitespace
	pecr7UnreadableXR       = "unreadable-xr"       // observed composite cannot be read
)

// pecr7Case captures the malformed XR shape drawn for one iteration.
type pecr7Case struct {
	kind        string
	tenant      string // used when kind involves a present-but-malformed tenant
	environment string // used when kind involves a present-but-malformed environment
}

// pecr7GenCase draws one malformed-XR shape. It covers: spec entirely absent,
// missing tenant key, empty/whitespace tenant, missing environment key,
// empty/whitespace environment, and an unreadable observed composite. For
// shapes that malform one field, the OTHER required field is well-formed so the
// failure is isolated to the intended field.
func pecr7GenCase(t *rapid.T) pecr7Case {
	kind := rapid.SampledFrom([]string{
		pecr7MissingSpec,
		pecr7MissingTenant,
		pecr7EmptyTenant,
		pecr7MissingEnvironment,
		pecr7EmptyEnvironment,
		pecr7UnreadableXR,
	}).Draw(t, "kind")

	c := pecr7Case{kind: kind}
	switch kind {
	case pecr7EmptyTenant:
		// Malformed tenant; well-formed environment.
		c.tenant = rapid.SampledFrom(pecr7BadStrings).Draw(t, "badTenant")
		c.environment = rapid.SampledFrom(pecr7Environments).Draw(t, "goodEnv")
	case pecr7MissingTenant:
		// Tenant key omitted; well-formed environment.
		c.environment = rapid.SampledFrom(pecr7Environments).Draw(t, "goodEnv")
	case pecr7EmptyEnvironment:
		// Well-formed tenant; malformed environment.
		c.tenant = pecr7GoodTenant().Draw(t, "goodTenant")
		c.environment = rapid.SampledFrom(pecr7BadStrings).Draw(t, "badEnv")
	case pecr7MissingEnvironment:
		// Well-formed tenant; environment key omitted.
		c.tenant = pecr7GoodTenant().Draw(t, "goodTenant")
	case pecr7MissingSpec:
		// Nothing else needed; the whole spec is omitted.
	case pecr7UnreadableXR:
		// Nothing else needed; the observed composite carries no resource.
	}
	return c
}

// pecr7BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment with apiVersion/kind present (so
// GetObservedCompositeResource can read it) but a spec that is malformed per
// the drawn case. For the pecr7UnreadableXR case, the observed composite is
// left without a resource so GetObservedCompositeResource fails and RunFunction
// takes the "cannot get observed composite resource" fatal path. Fields are
// OMITTED by not including the key — structpb.NewStruct rejects nil values, so
// absence is modelled by leaving the key out.
func pecr7BuildRequest(c pecr7Case) (*fnv1.RunFunctionRequest, error) {
	// An observed composite with no resource is unreadable — model an
	// unreadable XR by omitting the composite resource entirely.
	if c.kind == pecr7UnreadableXR {
		return &fnv1.RunFunctionRequest{
			Observed: &fnv1.State{
				Composite: &fnv1.Resource{},
			},
		}, nil
	}

	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name": "malformed",
		},
	}

	// Build the spec only when the case is not "spec entirely absent". Keys are
	// added conditionally so an omitted field is truly absent (not present-nil).
	if c.kind != pecr7MissingSpec {
		spec := map[string]any{}
		switch c.kind {
		case pecr7MissingTenant:
			// tenant omitted; environment present and valid.
			spec["environment"] = c.environment
		case pecr7EmptyTenant:
			// tenant present but empty/whitespace; environment present and valid.
			spec["tenant"] = c.tenant
			spec["environment"] = c.environment
		case pecr7MissingEnvironment:
			// environment omitted; tenant present and valid.
			spec["tenant"] = c.tenant
		case pecr7EmptyEnvironment:
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

// pecr7HasFatal reports whether the response results contain a fatal entry.
func pecr7HasFatal(rsp *fnv1.RunFunctionResponse) bool {
	for _, r := range rsp.GetResults() {
		if r.GetSeverity() == fnv1.Severity_SEVERITY_FATAL {
			return true
		}
	}
	return false
}

// Feature: tenant-environment-ecr, Property 7: Invalid spec emits zero resources
//
// For any TenantEnvironment XR whose required spec fields are missing or
// malformed — spec entirely absent, tenant key missing, tenant
// empty/whitespace, environment key missing, environment empty/whitespace — or
// whose observed composite is unreadable, RunFunction returns a response that
// (a) carries a FATAL result and (b) contains ZERO desired composed resources.
// The ECR Repository is therefore never emitted on the fatal path.
//
// Validates: Requirements 3.5, 7.1, 7.5
func TestPropertyECR7InvalidSpecFatalNoResources(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		c := pecr7GenCase(t)

		req, err := pecr7BuildRequest(c)
		if err != nil {
			t.Fatalf("pecr7BuildRequest(%+v) returned error: %v", c, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", c, err)
		}

		// (a) The response must carry a fatal result.
		if !pecr7HasFatal(rsp) {
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
