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

// pddb7Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb7Environments = []string{"dev", "staging", "prod"}

// pddb7Regions is the XRD region allow-list.
var pddb7Regions = []string{"ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "us-east-1"}

// pddb7ReadyCase enumerates the shapes of the observed Table's Ready condition
// the generator draws. Only readyTrue drives status.tableName to be set (and
// only then when the table is also enabled); readyFalse and readyAbsent both
// mean "not Ready" and leave the field unset.
type pddb7ReadyCase int

const (
	pddb7ReadyTrue pddb7ReadyCase = iota
	pddb7ReadyFalse
	pddb7ReadyAbsent
)

// pddb7TenantGenerator produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb7TenantGenerator() *rapid.Generator[string] {
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

// pddb7BuildRequest assembles a RunFunctionRequest whose observed composite is a
// TenantEnvironment with the given spec fields (including table.enabled), and
// whose observed composed resources contain a mocked bucket and a mocked Table.
//
// populateStatus sets status.tableName to the derived external name only when
// the table is enabled AND the observed keyTable resource reports Ready==True.
// The value is derived from naming (names.TableName), NOT read from atProvider,
// so this Table carries only a Ready condition per readyCase — no atProvider.
// The bucket is included (not Ready) so the observed set is realistic; its
// readiness does not affect status.tableName.
func pddb7BuildRequest(t *rapid.T, tenant, environment, region string, tableEnabled bool, readyCase pddb7ReadyCase) *fnv1.RunFunctionRequest {
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
				"enabled": tableEnabled,
			},
			"repository": map[string]any{
				"enabled": false,
			},
		},
	}

	composite, err := structpb.NewStruct(xr)
	if err != nil {
		t.Fatalf("pddb7BuildRequest: cannot build structpb for XR: %v", err)
	}

	// Observed bucket: present but not Ready. Its readiness is irrelevant to the
	// tableName property; it exists only so the observed set is realistic.
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
		t.Fatalf("pddb7BuildRequest: cannot build structpb for observed bucket: %v", err)
	}

	// Observed Table: a Ready condition per readyCase. readyAbsent omits the
	// condition entirely, so GetCondition(Ready) reports an unknown/empty
	// status that is not ConditionTrue.
	status := map[string]any{}
	switch readyCase {
	case pddb7ReadyTrue:
		status["conditions"] = []any{
			map[string]any{
				"type":   "Ready",
				"status": "True",
			},
		}
	case pddb7ReadyFalse:
		status["conditions"] = []any{
			map[string]any{
				"type":   "Ready",
				"status": "False",
			},
		}
	case pddb7ReadyAbsent:
		// No conditions at all.
	}
	tableObserved := map[string]any{
		"apiVersion": "dynamodb.aws.m.upbound.io/v1beta1",
		"kind":       "Table",
		"metadata": map[string]any{
			"namespace": tenant + "-" + environment,
		},
		"status": status,
	}
	tableStruct, err := structpb.NewStruct(tableObserved)
	if err != nil {
		t.Fatalf("pddb7BuildRequest: cannot build structpb for observed table: %v", err)
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
				string(keyTable): {
					Resource: tableStruct,
				},
			},
		},
	}
}

// pddb7DesiredStatusString reads a status.<field> string off the response's
// desired composite resource. It returns ("", false, nil) when the field is
// absent (fieldpath.IsNotFound), ("value", true, nil) when present, and a
// non-nil error only for unexpected read failures.
func pddb7DesiredStatusString(rsp *fnv1.RunFunctionResponse, field string) (string, bool, error) {
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

// Feature: tenant-environment-dynamodb, Property 7: status.tableName mirrors the external name only when Ready
//
// For any valid TenantEnvironment input paired with a mocked observed Table (a
// table.enabled flag and a Ready flag drawn from {True, False, absent}):
// status.tableName equals names.TableName (<tenant>-<env>-dtbl) byte-for-byte
// ONLY in the enabled + Ready==True case; in every other case (disabled, not
// observed as Ready, or Ready absent) status.tableName is absent. The value is
// derived from naming (like S3 status.bucketName), NOT read from the observed
// resource, and is re-derived from the current observed Ready condition every
// reconcile — never latched.
//
// Validates: Requirements 6.1, 6.2, 6.3, 6.4, 6.5, 6.6
func TestPropertyDdb7StatusTableName(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		tenant := pddb7TenantGenerator().Draw(t, "tenant")
		environment := rapid.SampledFrom(pddb7Environments).Draw(t, "environment")
		region := rapid.SampledFrom(pddb7Regions).Draw(t, "region")
		tableEnabled := rapid.Bool().Draw(t, "tableEnabled")
		readyCase := rapid.SampledFrom([]pddb7ReadyCase{
			pddb7ReadyTrue,
			pddb7ReadyFalse,
			pddb7ReadyAbsent,
		}).Draw(t, "readyCase")

		req := pddb7BuildRequest(t, tenant, environment, region, tableEnabled, readyCase)

		rsp, err := fn.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(tenant=%q, env=%q, enabled=%v, ready=%d) returned transport error: %v",
				tenant, environment, tableEnabled, readyCase, err)
		}

		gotName, nameSet, err := pddb7DesiredStatusString(rsp, "tableName")
		if err != nil {
			t.Fatalf("cannot read status.tableName (tenant=%q, env=%q, enabled=%v, ready=%d): %v",
				tenant, environment, tableEnabled, readyCase, err)
		}

		// status.tableName is set only when the table is enabled AND the
		// observed Table reports Ready==True. Every other combination
		// (disabled, Ready==False, or Ready absent) must leave the field
		// unset.
		wantName := tenant + "-" + environment + "-dtbl"
		wantSet := tableEnabled && readyCase == pddb7ReadyTrue

		if wantSet {
			if !nameSet {
				t.Fatalf("status.tableName unset; want %q (tenant=%q, env=%q, enabled=true, ready=True)",
					wantName, tenant, environment)
			}
			// Byte-for-byte equality with the derived external name: no
			// prefix, suffix, or whitespace added (R6.1, R6.4).
			if gotName != wantName {
				t.Fatalf("status.tableName = %q; want %q byte-for-byte (tenant=%q, env=%q)",
					gotName, wantName, tenant, environment)
			}
		} else {
			// Disabled (R6.5), Ready==False, or Ready absent (R6.2, R6.3,
			// R6.6): the field must be absent (or at worst empty).
			if nameSet && gotName != "" {
				t.Fatalf("status.tableName = %q; want unset (tenant=%q, env=%q, enabled=%v, ready=%d)",
					gotName, tenant, environment, tableEnabled, readyCase)
			}
		}
	})
}
