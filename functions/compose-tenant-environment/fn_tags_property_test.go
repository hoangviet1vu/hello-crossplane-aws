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

// p9Environments is the closed set of valid environment values per the XRD enum
// (dev/staging/prod).
var p9Environments = []string{"dev", "staging", "prod"}

// p9Regions is the XRD region allow-list. Any of these is valid for spec.region.
var p9Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// p9ExpectedTagKeys is the exact set of tag keys any tagged composed resource
// must carry — no more, no fewer.
var p9ExpectedTagKeys = []string{"environment", "managed-by", "tenant"}

// p9ManagedBy is the fixed value of the "managed-by" tag applied to every
// tagged composed resource.
const p9ManagedBy = "crossplane"

// p9TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func p9TenantGen() *rapid.Generator[string] {
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

// p9Spec captures the drawn TenantEnvironment spec inputs for one iteration.
type p9Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
	repoEnabled  bool
}

// p9GenSpec draws a full valid TenantEnvironment spec, varying every field
// (including the table/repository enabled flags) so the tag property is checked
// across the entire input space.
func p9GenSpec(t *rapid.T) p9Spec {
	return p9Spec{
		tenant:       p9TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(p9Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(p9Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
		repoEnabled:  rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// p9BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment (apiVersion platform.hello-crossplane.io/
// v1alpha1, kind TenantEnvironment) carrying the given spec.
func p9BuildRequest(s p9Spec) (*fnv1.RunFunctionRequest, error) {
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

// p9ReadTags reads spec.forProvider.tags off a desired composed resource's
// unstructured content. It returns the tag map and whether a tags field was
// present at all. A resource with no tags field (e.g. BucketVersioning, whose
// generated model has no tags) yields found=false — such resources are tolerated
// and skipped by the property, which only asserts the exact-key-set on resources
// that DO carry tags.
func p9ReadTags(dcd *resource.DesiredComposed) (map[string]string, bool) {
	content := dcd.Resource.UnstructuredContent()

	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return nil, false
	}
	forProvider, ok := spec["forProvider"].(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := forProvider["tags"].(map[string]any)
	if !ok {
		return nil, false
	}

	tags := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			// A non-string tag value is itself a violation of the standard tag
			// set; represent it so the assertion below can surface it.
			return nil, true
		}
		tags[k] = s
	}
	return tags, true
}

// p9SortedKeys returns the sorted keys of a tag map for stable comparison and
// readable failure messages.
func p9SortedKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// p9EqualStringSlices reports whether two string slices are element-wise equal.
func p9EqualStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Feature: tenant-environment-s3, Property 9: Composed resources carry exactly the standard tag set
//
// For any valid TenantEnvironment spec, every composed resource that CARRIES
// tags carries EXACTLY the keys {tenant, environment, managed-by} and no others,
// with tenant/environment values matching the XR spec and managed-by == crossplane.
// Resources that carry no tags at all (e.g. BucketVersioning, whose generated
// model has no tags field) are tolerated and skipped — the property only
// constrains resources that DO have tags.
//
// Validates: Requirements 6.5
func TestProperty9StandardTagSet(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := p9GenSpec(t)

		req, err := p9BuildRequest(s)
		if err != nil {
			t.Fatalf("p9BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		// Read the desired composed resources from the RESPONSE. function-sdk-go
		// v0.7.1 has no response.GetDesiredComposedResources, so reuse the
		// request reader over a request carrying the response's desired state.
		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources for spec %+v returned error: %v", s, err)
		}

		// At least one tagged resource must exist — the bucket always carries
		// tags — so the property is never vacuously satisfied.
		taggedSeen := false

		for name, dcd := range dcds {
			tags, hasTags := p9ReadTags(dcd)
			if !hasTags {
				// Resource carries no tags field (e.g. bucket-versioning);
				// tolerate its absence and move on.
				continue
			}
			taggedSeen = true

			// The key set must be EXACTLY {tenant, environment, managed-by}.
			gotKeys := p9SortedKeys(tags)
			if !p9EqualStringSlices(gotKeys, p9ExpectedTagKeys) {
				t.Fatalf("desired resource %q tags keys = %v; want exactly %v (spec=%+v)",
					name, gotKeys, p9ExpectedTagKeys, s)
			}

			// tenant/environment values must match the XR spec; managed-by is fixed.
			if tags["tenant"] != s.tenant {
				t.Fatalf("desired resource %q tags[tenant] = %q; want %q (spec=%+v)",
					name, tags["tenant"], s.tenant, s)
			}
			if tags["environment"] != s.environment {
				t.Fatalf("desired resource %q tags[environment] = %q; want %q (spec=%+v)",
					name, tags["environment"], s.environment, s)
			}
			if tags["managed-by"] != p9ManagedBy {
				t.Fatalf("desired resource %q tags[managed-by] = %q; want %q (spec=%+v)",
					name, tags["managed-by"], p9ManagedBy, s)
			}
		}

		if !taggedSeen {
			t.Fatalf("no composed resource carried tags for spec %+v; the bucket must always be tagged", s)
		}
	})
}
