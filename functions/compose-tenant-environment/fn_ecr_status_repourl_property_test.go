package main

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/fieldpath"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// pecr5Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pecr5Environments = []string{"dev", "staging", "prod"}

// pecr5Regions is the XRD region allow-list.
var pecr5Regions = []string{"ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "us-east-1"}

// pecr5URLCase enumerates the shapes of the observed
// status.atProvider.repositoryUrl the generator draws. Only wellFormed yields a
// value that must be mirrored into status.repositoryUrl; the rest (empty,
// whitespace-only, absent) are treated as "no value" and leave the field unset.
type pecr5URLCase int

const (
	pecr5URLWellFormed pecr5URLCase = iota
	pecr5URLEmpty
	pecr5URLWhitespace
	pecr5URLAbsent
)

// pecr5WellFormedURL is a representative well-formed ECR repository URL. Its
// exact value is asserted byte-for-byte, so it is a fixed constant rather than a
// generated string.
const pecr5WellFormedURL = "123456789012.dkr.ecr.ap-southeast-1.amazonaws.com/acme-dev-ecr"

// pecr5TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pecr5TenantGenerator() *rapid.Generator[string] {
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

// pecr5URLValue maps a URL case to the observed atProvider.repositoryUrl string
// and whether the field is present at all. The absent case returns present=false
// so the observed Repository status carries no repositoryUrl field.
func pecr5URLValue(c pecr5URLCase) (value string, present bool) {
	switch c {
	case pecr5URLWellFormed:
		return pecr5WellFormedURL, true
	case pecr5URLEmpty:
		return "", true
	case pecr5URLWhitespace:
		return "   \t ", true
	case pecr5URLAbsent:
		return "", false
	default:
		return "", false
	}
}

// pecr5BuildRequest assembles a RunFunctionRequest whose observed composite is a
// TenantEnvironment with the given spec fields (including repository.enabled),
// and whose observed composed resources contain a mocked bucket and a mocked
// Repository. Both carry a Ready condition; the Repository's readiness is driven
// by repoReady, and its status.atProvider.repositoryUrl is set per urlCase.
//
// populateStatus reads status.repositoryUrl off the observed keyRepository
// resource, so the Repository is keyed under keyRepository ("repository") and
// carries the Ready condition plus the drawn atProvider value. The bucket is
// included (not Ready) so the harness mirrors a realistic observed set; its
// readiness does not affect status.repositoryUrl.
func pecr5BuildRequest(t *rapid.T, tenant, environment, region string, repoEnabled, repoReady bool, urlCase pecr5URLCase) *fnv1.RunFunctionRequest {
	t.Helper()

	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name":      tenant + "-" + environment,
			"namespace": tenant + "-" + environment,
		},
		"spec": map[string]any{
			"tenant":      tenant,
			"environment": environment,
			"region":      region,
			"bucket": map[string]any{
				"versioning": true,
			},
			"table": map[string]any{
				"enabled": false,
			},
			"repository": map[string]any{
				"enabled": repoEnabled,
			},
		},
	}

	composite, err := structpb.NewStruct(xr)
	if err != nil {
		t.Fatalf("pecr5BuildRequest: cannot build structpb for XR: %v", err)
	}

	// Observed bucket: present but not Ready. Its readiness is irrelevant to the
	// repositoryUrl property; it exists only so the observed set is realistic.
	bucketObserved := map[string]any{
		"apiVersion": "s3.aws.m.upbound.io/v1beta1",
		"kind":       "Bucket",
		"metadata": map[string]any{
			"namespace": tenant + "-" + environment,
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": "False",
				},
			},
		},
	}
	bucketStruct, err := structpb.NewStruct(bucketObserved)
	if err != nil {
		t.Fatalf("pecr5BuildRequest: cannot build structpb for observed bucket: %v", err)
	}

	// Observed Repository: Ready condition per repoReady, atProvider per urlCase.
	repoReadyStatus := "False"
	if repoReady {
		repoReadyStatus = "True"
	}
	atProvider := map[string]any{}
	if value, present := pecr5URLValue(urlCase); present {
		atProvider["repositoryUrl"] = value
	}
	repoObserved := map[string]any{
		"apiVersion": "ecr.aws.m.upbound.io/v1beta1",
		"kind":       "Repository",
		"metadata": map[string]any{
			"namespace": tenant + "-" + environment,
		},
		"status": map[string]any{
			"atProvider": atProvider,
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": repoReadyStatus,
				},
			},
		},
	}
	repoStruct, err := structpb.NewStruct(repoObserved)
	if err != nil {
		t.Fatalf("pecr5BuildRequest: cannot build structpb for observed repository: %v", err)
	}

	return &fnv1.RunFunctionRequest{
		Observed: &fnv1.State{
			Composite: &fnv1.Resource{
				Resource: composite,
			},
			Resources: map[string]*fnv1.Resource{
				string(keyBucket): {
					Resource: bucketStruct,
				},
				string(keyRepository): {
					Resource: repoStruct,
				},
			},
		},
	}
}

// pecr5DesiredStatusString reads a status.<field> string off the response's
// desired composite resource. It returns ("", false, nil) when the field is
// absent (fieldpath.IsNotFound), ("value", true, nil) when present, and a
// non-nil error only for unexpected read failures.
func pecr5DesiredStatusString(rsp *fnv1.RunFunctionResponse, field string) (string, bool, error) {
	dxr, err := request.GetDesiredCompositeResource(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
	if err != nil {
		return "", false, err
	}
	v, err := fieldpath.Pave(dxr.Resource.Object).GetString("status." + field)
	if err != nil {
		if fieldpath.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

// Feature: tenant-environment-ecr, Property 5: Status repositoryUrl mirrors observed atProvider value only when Ready
//
// For any valid TenantEnvironment input paired with a mocked observed Repository
// (a repository.enabled flag, a Ready flag, and an atProvider.repositoryUrl
// drawn from {well-formed, empty, whitespace-only, absent}): status.repositoryUrl
// equals the observed atProvider.repositoryUrl byte-for-byte ONLY in the enabled
// + Ready + non-whitespace case; in every other case (disabled, not Ready, or
// missing/whitespace-only observed value) status.repositoryUrl is absent.
//
// Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7
func TestPropertyEcr5StatusRepositoryURL(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		tenant := pecr5TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(pecr5Environments).Draw(t, "environment")
		region := rapid.SampledFrom(pecr5Regions).Draw(t, "region")
		repoEnabled := rapid.Bool().Draw(t, "repoEnabled")
		repoReady := rapid.Bool().Draw(t, "repoReady")
		urlCase := rapid.SampledFrom([]pecr5URLCase{
			pecr5URLWellFormed,
			pecr5URLEmpty,
			pecr5URLWhitespace,
			pecr5URLAbsent,
		}).Draw(t, "urlCase")

		req := pecr5BuildRequest(t, tenant, environment, region, repoEnabled, repoReady, urlCase)

		rsp, err := fn.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(tenant=%q, env=%q, enabled=%v, ready=%v, url=%d) returned transport error: %v",
				tenant, environment, repoEnabled, repoReady, urlCase, err)
		}

		gotURL, urlSet, err := pecr5DesiredStatusString(rsp, "repositoryUrl")
		if err != nil {
			t.Fatalf("cannot read status.repositoryUrl (tenant=%q, env=%q, enabled=%v, ready=%v, url=%d): %v",
				tenant, environment, repoEnabled, repoReady, urlCase, err)
		}

		// status.repositoryUrl is set only when the repository is enabled AND
		// the observed Repository is Ready AND the observed atProvider value is
		// present and non-whitespace (the well-formed case). Every other
		// combination must leave the field absent.
		wantSet := repoEnabled && repoReady && urlCase == pecr5URLWellFormed

		if wantSet {
			if !urlSet {
				t.Fatalf("status.repositoryUrl unset; want %q (tenant=%q, env=%q, enabled=true, ready=true, url=well-formed)",
					pecr5WellFormedURL, tenant, environment)
			}
			// Byte-for-byte equality with the observed atProvider value: no
			// prefix, suffix, or whitespace added (R5.1, R5.4).
			if gotURL != pecr5WellFormedURL {
				t.Fatalf("status.repositoryUrl = %q; want %q byte-for-byte (tenant=%q, env=%q)",
					gotURL, pecr5WellFormedURL, tenant, environment)
			}
		} else {
			// Disabled (R5.5), not Ready (R5.2, R5.3, R5.7), or missing/
			// whitespace-only observed value (R5.6): the field must be absent
			// (or at worst empty).
			if urlSet && gotURL != "" {
				t.Fatalf("status.repositoryUrl = %q; want unset (tenant=%q, env=%q, enabled=%v, ready=%v, url=%d)",
					gotURL, tenant, environment, repoEnabled, repoReady, urlCase)
			}
		}
	})
}
