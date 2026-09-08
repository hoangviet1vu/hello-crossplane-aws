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

// p10Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var p10Environments = []string{"dev", "staging", "prod"}

// p10Regions is the XRD region allow-list.
var p10Regions = []string{"ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "us-east-1"}

// p10TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func p10TenantGenerator() *rapid.Generator[string] {
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

// p10BuildRequest assembles a RunFunctionRequest whose observed composite is a
// TenantEnvironment with the given spec fields, and whose observed composed
// resources contain a mocked bucket carrying a Ready condition set according to
// bucketReady. The observed bucket's Ready condition drives status population:
// the SDK reads req.Observed.Resources back via
// request.GetObservedComposedResources, and populateStatus consults the bucket's
// Ready condition through the composed-resource wrapper's GetCondition.
func p10BuildRequest(t *rapid.T, tenant, environment, region string, versioning, tableEnabled, repoEnabled, bucketReady bool) *fnv1.RunFunctionRequest {
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
				"versioning": versioning,
			},
			"table": map[string]any{
				"enabled": tableEnabled,
			},
			"repository": map[string]any{
				"enabled": repoEnabled,
			},
		},
	}

	composite, err := structpb.NewStruct(xr)
	if err != nil {
		t.Fatalf("p10BuildRequest: cannot build structpb for XR: %v", err)
	}

	// Model the observed composed bucket with a Ready condition whose status is
	// "True" or "False" according to bucketReady. The AWS group/kind mirror the
	// resource the function itself emits.
	readyStatus := "False"
	if bucketReady {
		readyStatus = "True"
	}
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
					"status": readyStatus,
				},
			},
		},
	}

	bucketStruct, err := structpb.NewStruct(bucketObserved)
	if err != nil {
		t.Fatalf("p10BuildRequest: cannot build structpb for observed bucket: %v", err)
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
			},
		},
	}
}

// p10DesiredStatusString reads a status.<field> string off the response's
// desired composite resource. It returns ("", false, nil) when the field is
// absent (fieldpath.IsNotFound), ("value", true, nil) when present, and a
// non-nil error only for unexpected read failures.
func p10DesiredStatusString(rsp *fnv1.RunFunctionResponse, field string) (string, bool, error) {
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

// Feature: tenant-environment-s3, Property 10: Status bucket name reflects current bucket readiness
//
// For any valid TenantEnvironment input paired with a mocked observed-bucket
// readiness flag: when the observed bucket reports Ready the function sets
// status.bucketName to exactly <tenant>-<env>-bucket (no prefix, suffix, or
// whitespace); when the observed bucket is not Ready, status.bucketName is left
// unset; and in all cases status.tableName and status.repositoryUrl remain
// unset.
//
// Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6
func TestProperty10StatusBucketNameReflectsReadiness(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		tenant := p10TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(p10Environments).Draw(t, "environment")
		region := rapid.SampledFrom(p10Regions).Draw(t, "region")
		versioning := rapid.Bool().Draw(t, "versioning")
		tableEnabled := rapid.Bool().Draw(t, "tableEnabled")
		repoEnabled := rapid.Bool().Draw(t, "repoEnabled")
		bucketReady := rapid.Bool().Draw(t, "bucketReady")

		req := p10BuildRequest(t, tenant, environment, region, versioning, tableEnabled, repoEnabled, bucketReady)

		rsp, err := fn.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(tenant=%q, env=%q, ready=%v) returned transport error: %v",
				tenant, environment, bucketReady, err)
		}

		wantBucketExtName := tenant + "-" + environment + "-bucket"

		gotBucketName, bucketNameSet, err := p10DesiredStatusString(rsp, "bucketName")
		if err != nil {
			t.Fatalf("cannot read status.bucketName (tenant=%q, env=%q, ready=%v): %v",
				tenant, environment, bucketReady, err)
		}

		if bucketReady {
			// When the observed bucket is Ready, status.bucketName must equal
			// the external name exactly, with no extra characters (R5.1, R5.4).
			if !bucketNameSet {
				t.Fatalf("status.bucketName unset but bucket is Ready (tenant=%q, env=%q); want %q",
					tenant, environment, wantBucketExtName)
			}
			if gotBucketName != wantBucketExtName {
				t.Fatalf("status.bucketName = %q; want %q (tenant=%q, env=%q, ready=true)",
					gotBucketName, wantBucketExtName, tenant, environment)
			}
		} else {
			// When the observed bucket is not Ready, status.bucketName must be
			// unset (absent or empty) (R5.2, R5.3).
			if bucketNameSet && gotBucketName != "" {
				t.Fatalf("status.bucketName = %q; want unset (tenant=%q, env=%q, ready=false)",
					gotBucketName, tenant, environment)
			}
		}

		// In all cases, status.tableName and status.repositoryUrl remain unset
		// in this slice (R5.5, R5.6).
		gotTableName, tableNameSet, err := p10DesiredStatusString(rsp, "tableName")
		if err != nil {
			t.Fatalf("cannot read status.tableName (tenant=%q, env=%q, ready=%v): %v",
				tenant, environment, bucketReady, err)
		}
		if tableNameSet && gotTableName != "" {
			t.Fatalf("status.tableName = %q; want unset (tenant=%q, env=%q, ready=%v)",
				gotTableName, tenant, environment, bucketReady)
		}

		gotRepoURL, repoURLSet, err := p10DesiredStatusString(rsp, "repositoryUrl")
		if err != nil {
			t.Fatalf("cannot read status.repositoryUrl (tenant=%q, env=%q, ready=%v): %v",
				tenant, environment, bucketReady, err)
		}
		if repoURLSet && gotRepoURL != "" {
			t.Fatalf("status.repositoryUrl = %q; want unset (tenant=%q, env=%q, ready=%v)",
				gotRepoURL, tenant, environment, bucketReady)
		}
	})
}
