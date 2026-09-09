package main

import (
	"context"
	"sort"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
)

// repoFlag enumerates the three states spec.repository.enabled can take on an
// observed XR: explicitly true, explicitly false, or the whole repository block
// absent (schema default = false).
type repoFlag int

const (
	repoEnabledTrue repoFlag = iota
	repoEnabledFalse
	repoAbsent
)

// pEcr1KeysDisabled is the exact desired-resources key set when the repository
// is disabled or absent: only the S3 bucket and its versioning resource.
var pEcr1KeysDisabled = []resource.Name{keyBucket, keyBucketVersioning}

// pEcr1KeysEnabled is the exact desired-resources key set when the repository
// is enabled: the S3 bucket, its versioning resource, and the ECR repository.
var pEcr1KeysEnabled = []resource.Name{keyBucket, keyBucketVersioning, keyRepository}

// pEcr1Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// It generalises the fn_exactly_two harness over the repository flag: repo
// varies across true/false/absent, while table.enabled varies freely to prove
// no Table key is ever emitted regardless.
type pEcr1Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repo         repoFlag
}

// pEcr1GenSpec draws a full valid TenantEnvironment spec, varying
// repository.enabled across all three states (true/false/absent) and
// table.enabled freely, alongside tenant, environment, region, and
// bucket.versioning. It reuses the tenant/environment/region generators from
// the fn_exactly_two harness.
func pEcr1GenSpec(t *rapid.T) pEcr1Spec {
	return pEcr1Spec{
		tenant:       p8TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(p8Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(p8Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repo:         rapid.SampledFrom([]repoFlag{repoEnabledTrue, repoEnabledFalse, repoAbsent}).Draw(t, "repo"),
	}
}

// pEcr1BuildRequest constructs a RunFunctionRequest whose observed composite is
// a TenantEnvironment carrying the given spec. When repo is repoAbsent, the
// spec.repository block is omitted entirely so the function exercises the
// fieldpath.IsNotFound (schema-default false) path.
func pEcr1BuildRequest(s pEcr1Spec) (*fnv1.RunFunctionRequest, error) {
	spec := map[string]any{
		"tenant":      s.tenant,
		"environment": s.environment,
		"region":      s.region,
		"bucket": map[string]any{
			"versioning": s.versioning,
		},
		"table": map[string]any{
			"enabled": s.tableEnabled,
		},
	}
	switch s.repo {
	case repoEnabledTrue:
		spec["repository"] = map[string]any{"enabled": true}
	case repoEnabledFalse:
		spec["repository"] = map[string]any{"enabled": false}
	case repoAbsent:
		// Leave spec.repository unset entirely.
	}

	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name":      s.tenant + "-" + s.environment,
			"namespace": s.tenant + "-" + s.environment,
		},
		"spec": spec,
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

// Feature: tenant-environment-ecr, Property 1: Resource count is exactly two
// when disabled, exactly three when enabled
//
// For any valid TenantEnvironment spec, RunFunction returns a desired composed
// resources map whose key set is EXACTLY {"bucket", "bucket-versioning"} when
// spec.repository.enabled is false or absent, plus "repository" when it is true.
// table.enabled varies freely: since the DynamoDB slice it adds exactly the
// "table" key when true and never when false/absent, so this harness asserts the
// ECR keyset holds across every table.enabled value (the table-specific keyset
// invariant is owned by P-DDB-1 in fn_ddb_keyset_property_test.go).
//
// Validates: Requirements 2.1, 2.2, 2.3, 6.1, 6.2, 6.3, 6.4, 6.5
func TestPropertyEcr1ExactKeyset(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pEcr1GenSpec(t)

		req, err := pEcr1BuildRequest(s)
		if err != nil {
			t.Fatalf("pEcr1BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		// Read the desired composed resources from the RESPONSE. function-sdk-go
		// v0.7.1 has no response.GetDesiredComposedResources, so reuse the
		// request reader over a request carrying the response's desired state.
		resources, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources for spec %+v returned error: %v", s, err)
		}

		// Copy the base key set so appending the table key never mutates the
		// shared package-level slices.
		var want []resource.Name
		if s.repo == repoEnabledTrue {
			want = append(want, pEcr1KeysEnabled...)
		} else {
			want = append(want, pEcr1KeysDisabled...)
		}
		// Since the DynamoDB slice, table.enabled=true adds the "table" key.
		// This harness draws tableEnabled freely, so account for that key when
		// it is set (mirrors the P-DDB-1 keyset logic).
		if s.tableEnabled {
			want = append(want, keyTable)
		}

		// The key set must be EXACTLY the expected keys — no more, no fewer.
		if len(resources) != len(want) {
			t.Fatalf("desired composed resources for spec %+v: expected exactly %d resources %v, got %d with keys %v",
				s, len(want), want, len(resources), pEcr1Keys(resources))
		}

		for _, k := range want {
			if _, ok := resources[k]; !ok {
				t.Fatalf("desired composed resources for spec %+v missing expected key %q; got keys %v",
					s, k, pEcr1Keys(resources))
			}
		}

		// The "table" key appears iff table.enabled was drawn true.
		if _, ok := resources[keyTable]; ok != s.tableEnabled {
			t.Fatalf("desired composed resources for spec %+v: %q key present=%v, want present=%v; got keys %v",
				s, keyTable, ok, s.tableEnabled, pEcr1Keys(resources))
		}
	})
}

// pEcr1Keys returns the sorted string form of a desired-composed key set for
// stable comparison and readable failure messages.
func pEcr1Keys(resources map[resource.Name]*resource.DesiredComposed) []string {
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}
