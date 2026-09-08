package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
)

// pecr6Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pecr6Environments = []string{"dev", "staging", "prod"}

// pecr6Regions is the XRD region allow-list.
var pecr6Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pecr6ManagedBy is the fixed value of the "managed-by" tag applied to the
// composed Repository.
const pecr6ManagedBy = "crossplane"

// pecr6TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pecr6TenantGen() *rapid.Generator[string] {
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

// pecr6Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// repository.enabled is varied so the property checks both the enabled (tag set
// present and exact) and disabled (repository key absent) behaviors.
type pecr6Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repoEnabled  bool
}

// pecr6GenSpec draws a valid TenantEnvironment spec, varying repository.enabled
// (and table.enabled, S3 fields) across the input space.
func pecr6GenSpec(t *rapid.T) pecr6Spec {
	return pecr6Spec{
		tenant:       pecr6TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(pecr6Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(pecr6Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repoEnabled:  rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// pecr6BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec.
func pecr6BuildRequest(s pecr6Spec) (*fnv1.RunFunctionRequest, error) {
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

// pecr6ReadTags reads spec.forProvider.tags off a desired composed resource's
// unstructured content, returning the tag map as a string->string map. A missing
// or malformed tags field yields an empty map, which the assertion below then
// reports as a key-set mismatch.
func pecr6ReadTags(dcd *resource.DesiredComposed) map[string]string {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	raw, ok := forProvider["tags"].(map[string]any)
	if !ok {
		return map[string]string{}
	}

	tags := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			// A non-string tag value violates the standard tag set; surface it
			// with a sentinel so the exact-key/value comparison below fails.
			tags[k] = "<non-string>"
			continue
		}
		tags[k] = s
	}
	return tags
}

// Feature: tenant-environment-ecr, Property 6: The composed Repository carries exactly the three standard tags
//
// For any valid TenantEnvironment spec with repository.enabled: true, the
// composed "repository" desired resource carries spec.forProvider.tags with
// EXACTLY the keys {environment, managed-by, tenant} — no more, no fewer — with
// tenant/environment equal to the XR spec fields and managed-by == crossplane.
// When repository.enabled is false, no "repository" key is emitted at all.
//
// Validates: Requirements 4.1, 4.2, 4.3, 4.4, 6.8
func TestPropertyECR6RepositoryTagSet(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pecr6GenSpec(t)

		req, err := pecr6BuildRequest(s)
		if err != nil {
			t.Fatalf("pecr6BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		repo, ok := dcds[keyRepository]

		if !s.repoEnabled {
			// Disabled: the repository key must be absent entirely.
			if ok {
				t.Fatalf("repository.enabled=false but desired resources contain a %q key (spec=%+v)", keyRepository, s)
			}
			return
		}

		// Enabled: the repository key must exist and carry exactly the three
		// standard tags.
		if !ok {
			t.Fatalf("repository.enabled=true but desired resources missing %q key (spec=%+v)", keyRepository, s)
		}

		want := map[string]string{
			"tenant":      s.tenant,
			"environment": s.environment,
			"managed-by":  pecr6ManagedBy,
		}
		got := pecr6ReadTags(repo)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("repository spec.forProvider.tags mismatch (-want +got):\n%s\n(spec=%+v)", diff, s)
		}
	})
}
