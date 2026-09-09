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

// tableFlag enumerates the three states spec.table.enabled can take on an
// observed XR: explicitly true, explicitly false, or the whole table block
// absent (schema default = false).
type tableFlag int

const (
	tableEnabledTrue tableFlag = iota
	tableEnabledFalse
	tableAbsent
)

// pDdb1Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// It generalises the fn_ecr_keyset harness over the table flag: table varies
// across true/false/absent, while repository.enabled also varies across all
// three states, so the exact keyset is proved for every flag combination.
type pDdb1Spec struct {
	tenant      string
	environment string
	region      string
	versioning  bool
	table       tableFlag
	repo        repoFlag
}

// pDdb1GenSpec draws a full valid TenantEnvironment spec, varying
// table.enabled across all three states (true/false/absent) and
// repository.enabled across all three states, alongside tenant, environment,
// region, and bucket.versioning. It reuses the tenant/environment/region
// generators from the fn_exactly_two harness.
func pDdb1GenSpec(t *rapid.T) pDdb1Spec {
	return pDdb1Spec{
		tenant:      p8TenantGen().Draw(t, "tenant"),
		environment: rapid.SampledFrom(p8Environments).Draw(t, "environment"),
		region:      rapid.SampledFrom(p8Regions).Draw(t, "region"),
		versioning:  rapid.Bool().Draw(t, "versioning"),
		table:       rapid.SampledFrom([]tableFlag{tableEnabledTrue, tableEnabledFalse, tableAbsent}).Draw(t, "table"),
		repo:        rapid.SampledFrom([]repoFlag{repoEnabledTrue, repoEnabledFalse, repoAbsent}).Draw(t, "repo"),
	}
}

// pDdb1BuildRequest constructs a RunFunctionRequest whose observed composite is
// a TenantEnvironment carrying the given spec. When table/repo is absent, the
// corresponding spec block is omitted entirely so the function exercises the
// fieldpath.IsNotFound (schema-default false) path.
func pDdb1BuildRequest(s pDdb1Spec) (*fnv1.RunFunctionRequest, error) {
	spec := map[string]any{
		"tenant":      s.tenant,
		"environment": s.environment,
		"region":      s.region,
		"bucket": map[string]any{
			"versioning": s.versioning,
		},
	}
	switch s.table {
	case tableEnabledTrue:
		spec["table"] = map[string]any{"enabled": true}
	case tableEnabledFalse:
		spec["table"] = map[string]any{"enabled": false}
	case tableAbsent:
		// Leave spec.table unset entirely.
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

// pDdb1WantKeys computes the exact desired-resources key set for a drawn spec:
// always {bucket, bucket-versioning}, plus repository iff repo is enabled, plus
// table iff table is enabled.
func pDdb1WantKeys(s pDdb1Spec) []resource.Name {
	want := []resource.Name{keyBucket, keyBucketVersioning}
	if s.repo == repoEnabledTrue {
		want = append(want, keyRepository)
	}
	if s.table == tableEnabledTrue {
		want = append(want, keyTable)
	}
	return want
}

// Feature: tenant-environment-dynamodb, Property 1: Exact resource keyset
// reflects the enabled flags
//
// For any valid TenantEnvironment spec, RunFunction returns a desired composed
// resources map whose key set is EXACTLY {"bucket", "bucket-versioning"} plus
// "repository" iff spec.repository.enabled is true and plus "table" iff
// spec.table.enabled is true — and no other key. When table.enabled is false or
// absent, no "table" key is ever emitted.
//
// Validates: Requirements 2.1, 2.2, 2.3, 2.7, 5.4, 7.4, 7.5, 7.6
func TestPropertyDdb1ExactKeyset(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pDdb1GenSpec(t)

		req, err := pDdb1BuildRequest(s)
		if err != nil {
			t.Fatalf("pDdb1BuildRequest(%+v) returned error: %v", s, err)
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

		want := pDdb1WantKeys(s)

		// The key set must be EXACTLY the expected keys — no more, no fewer.
		if len(resources) != len(want) {
			t.Fatalf("desired composed resources for spec %+v: expected exactly %d resources %v, got %d with keys %v",
				s, len(want), pDdb1SortKeys(want), len(resources), pDdb1Keys(resources))
		}

		for _, k := range want {
			if _, ok := resources[k]; !ok {
				t.Fatalf("desired composed resources for spec %+v missing expected key %q; got keys %v",
					s, k, pDdb1Keys(resources))
			}
		}

		// When the table is disabled or absent, the "table" key must never
		// appear (R2.2, R2.3, R7.5).
		if s.table != tableEnabledTrue {
			if _, ok := resources[keyTable]; ok {
				t.Fatalf("desired composed resources for spec %+v unexpectedly contains a %q key; got keys %v",
					s, keyTable, pDdb1Keys(resources))
			}
		}
	})
}

// pDdb1Keys returns the sorted string form of a desired-composed key set for
// stable comparison and readable failure messages.
func pDdb1Keys(resources map[resource.Name]*resource.DesiredComposed) []string {
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}

// pDdb1SortKeys returns the sorted string form of an expected key set for
// readable failure messages.
func pDdb1SortKeys(names []resource.Name) []string {
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, string(n))
	}
	sort.Strings(keys)
	return keys
}
