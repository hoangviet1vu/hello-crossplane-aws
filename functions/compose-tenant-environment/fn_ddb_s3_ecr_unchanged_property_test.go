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

// pddb11Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb11Environments = []string{"dev", "staging", "prod"}

// pddb11Regions is the XRD region allow-list.
var pddb11Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb11BillingModes is the closed set of valid billing modes per the XRD enum.
var pddb11BillingModes = []string{"PAY_PER_REQUEST", "PROVISIONED"}

// pddb11TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb11TenantGen() *rapid.Generator[string] {
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

// pddb11HashKeyGen produces a non-empty lowercase-alphanumeric identifier for
// spec.table.hashKey. Its exact value must not influence the S3/ECR output, so
// varying it exercises the invariance claim.
func pddb11HashKeyGen() *rapid.Generator[string] {
	char := rapid.SampledFrom([]byte("abcdefghijklmnopqrstuvwxyz0123456789"))
	return rapid.Custom(func(t *rapid.T) string {
		b := rapid.SliceOfN(char, 1, 12).Draw(t, "hashKeyChars")
		return string(b)
	})
}

// pddb11Spec captures the drawn TenantEnvironment inputs that are held CONSTANT
// across the two RunFunction invocations. Only the spec.table.* block differs
// between the two runs; every field here is identical in both.
type pddb11Spec struct {
	tenant      string
	environment string
	region      string
	versioning  bool
	repoEnabled bool
	// Table inputs used only for the "table enabled" run.
	hashKey     string
	billingMode string
}

// pddb11GenSpec draws the shared TenantEnvironment inputs plus the table inputs
// used only by the enabled run. repository.enabled is varied so the invariance
// is proved for the ECR "repository" entry as well as the S3 entries.
func pddb11GenSpec(t *rapid.T) pddb11Spec {
	return pddb11Spec{
		tenant:      pddb11TenantGen().Draw(t, "tenant"),
		environment: rapid.SampledFrom(pddb11Environments).Draw(t, "environment"),
		region:      rapid.SampledFrom(pddb11Regions).Draw(t, "region"),
		versioning:  rapid.Bool().Draw(t, "versioning"),
		repoEnabled: rapid.Bool().Draw(t, "repoEnabled"),
		hashKey:     pddb11HashKeyGen().Draw(t, "hashKey"),
		billingMode: rapid.SampledFrom(pddb11BillingModes).Draw(t, "billingMode"),
	}
}

// pddb11BuildRequest constructs a RunFunctionRequest for a TenantEnvironment
// carrying the shared spec. When tableEnabled is true the spec.table block sets
// enabled=true with the drawn hashKey/billingMode; when false the spec.table
// block is omitted entirely (schema-default disabled). Everything else — tenant,
// environment, region, bucket.versioning, repository.enabled — is identical in
// both runs, so any difference in the S3/ECR entries would be attributable only
// to spec.table.*.
func pddb11BuildRequest(s pddb11Spec, tableEnabled bool) (*fnv1.RunFunctionRequest, error) {
	spec := map[string]any{
		"tenant":      s.tenant,
		"environment": s.environment,
		"region":      s.region,
		"bucket": map[string]any{
			"versioning": s.versioning,
		},
		"repository": map[string]any{
			"enabled": s.repoEnabled,
		},
	}
	if tableEnabled {
		spec["table"] = map[string]any{
			"enabled":     true,
			"hashKey":     s.hashKey,
			"billingMode": s.billingMode,
		}
	}
	// When tableEnabled is false, spec.table is left unset entirely.

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

// pddb11Run executes RunFunction for the given spec + table flag and returns the
// resulting desired composed resources map.
func pddb11Run(f *Function, s pddb11Spec, tableEnabled bool) (map[resource.Name]*resource.DesiredComposed, error) {
	req, err := pddb11BuildRequest(s, tableEnabled)
	if err != nil {
		return nil, err
	}
	rsp, err := f.RunFunction(context.Background(), req)
	if err != nil {
		return nil, err
	}
	// function-sdk-go v0.7.1 has no response.GetDesiredComposedResources, so
	// reuse the request reader over a request carrying the response's desired
	// state (matches the sibling property harnesses).
	return request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
}

// pddb11Content projects a desired composed resource to its raw unstructured
// content for a byte-faithful, whole-object comparison. Nil-safe so a missing
// entry compares cleanly against another missing entry.
func pddb11Content(dcd *resource.DesiredComposed) map[string]any {
	if dcd == nil {
		return nil
	}
	return dcd.Resource.UnstructuredContent()
}

// Feature: tenant-environment-dynamodb, Property 11: S3 and ECR behavior is
// unchanged
//
// For any valid TenantEnvironment input, the composed "bucket",
// "bucket-versioning", and (where spec.repository.enabled is true) "repository"
// resources are byte-for-byte identical regardless of the spec.table.* values.
// RunFunction is invoked twice on inputs that differ ONLY in the spec.table.*
// block — once with the table disabled (spec.table omitted) and once with it
// enabled (varying hashKey/billingMode) — and the S3/ECR entries extracted from
// each desired map are compared with go-cmp. The "table" entry, present in the
// enabled run only, is excluded from the comparison.
//
// Validates: Requirements 7.1, 7.2, 7.3
func TestPropertyDDB11S3AndECRUnchanged(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb11GenSpec(t)

		disabled, err := pddb11Run(f, s, false)
		if err != nil {
			t.Fatalf("RunFunction (table disabled) for spec %+v returned error: %v", s, err)
		}
		enabled, err := pddb11Run(f, s, true)
		if err != nil {
			t.Fatalf("RunFunction (table enabled) for spec %+v returned error: %v", s, err)
		}

		// Sanity: the enabled run must actually add a table key, and the
		// disabled run must not — otherwise the invariance comparison would be
		// vacuous.
		if _, ok := enabled[keyTable]; !ok {
			t.Fatalf("table-enabled run for spec %+v did not emit the %q key", s, keyTable)
		}
		if _, ok := disabled[keyTable]; ok {
			t.Fatalf("table-disabled run for spec %+v unexpectedly emitted the %q key", s, keyTable)
		}

		// The S3 entries are always present in both runs; the ECR "repository"
		// entry is present only when repository.enabled is true. Every one of
		// these entries must be byte-for-byte identical across the two runs.
		keysToCompare := []resource.Name{keyBucket, keyBucketVersioning}
		if s.repoEnabled {
			keysToCompare = append(keysToCompare, keyRepository)
		}

		for _, k := range keysToCompare {
			got, gotOK := enabled[k]
			want, wantOK := disabled[k]
			if gotOK != wantOK {
				t.Fatalf("resource %q presence differs across table flag for spec %+v: enabled=%v, disabled=%v",
					k, s, gotOK, wantOK)
			}
			if diff := cmp.Diff(pddb11Content(want), pddb11Content(got)); diff != "" {
				t.Fatalf("resource %q differs between table-disabled and table-enabled runs for spec %+v (-disabled +enabled):\n%s",
					k, s, diff)
			}
		}
	})
}
