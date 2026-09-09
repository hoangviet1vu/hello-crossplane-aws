package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	"pgregory.net/rapid"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// pddb2NamespacedDynamoDBAPIVersion is the ONLY apiVersion the composed Table
// may carry: the namespaced (.m.) API group. The legacy cluster-scoped group
// dynamodb.aws.upbound.io is wrong for a namespaced XR and must never appear.
const pddb2NamespacedDynamoDBAPIVersion = "dynamodb.aws.m.upbound.io/v1beta1"

// pddb2LegacyDynamoDBGroup is the legacy cluster-scoped DynamoDB group prefix
// that must never appear on the composed Table.
const pddb2LegacyDynamoDBGroup = "dynamodb.aws.upbound.io"

// pddb2Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pddb2Environments = []string{"dev", "staging", "prod"}

// pddb2Regions is the XRD region allow-list.
var pddb2Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pddb2TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pddb2TenantGen() *rapid.Generator[string] {
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

// pddb2Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// table.enabled is fixed true so a Table is always emitted; the other gated flag
// (repository.enabled) and the S3 fields are varied to check the group and Kind
// hold across the input space.
type pddb2Spec struct {
	tenant      string
	environment string
	region      string
	versioning  bool
	repoEnabled bool
}

// pddb2GenSpec draws a valid TenantEnvironment spec with table.enabled always
// true.
func pddb2GenSpec(t *rapid.T) pddb2Spec {
	return pddb2Spec{
		tenant:      pddb2TenantGen().Draw(t, "tenant"),
		environment: rapid.SampledFrom(pddb2Environments).Draw(t, "environment"),
		region:      rapid.SampledFrom(pddb2Regions).Draw(t, "region"),
		versioning:  rapid.Bool().Draw(t, "versioning"),
		repoEnabled: rapid.Bool().Draw(t, "repoEnabled"),
	}
}

// pddb2BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// table.enabled: true.
func pddb2BuildRequest(s pddb2Spec) (*fnv1.RunFunctionRequest, error) {
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
				"enabled": true,
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

// Feature: tenant-environment-dynamodb, Property 2: Table group and Kind are correct
//
// For any valid TenantEnvironment spec with table.enabled: true, RunFunction
// emits a "table" desired resource carrying apiVersion
// dynamodb.aws.m.upbound.io/v1beta1 and Kind Table. The legacy cluster-scoped
// group dynamodb.aws.upbound.io must never appear on it.
//
// Validates: Requirements 2.4, 7.7
func TestPropertyDDB2TableGroupAndKind(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pddb2GenSpec(t)

		req, err := pddb2BuildRequest(s)
		if err != nil {
			t.Fatalf("pddb2BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		// The "table" entry must exist when enabled.
		table, ok := dcds[keyTable]
		if !ok {
			t.Fatalf("desired resources missing %q key with table.enabled=true (spec=%+v)", keyTable, s)
		}

		// It must use the namespaced DynamoDB apiVersion, never the legacy group.
		gotAPIVersion := table.Resource.GetAPIVersion()
		if gotAPIVersion != pddb2NamespacedDynamoDBAPIVersion {
			t.Fatalf("desired resource %q apiVersion = %q; want %q (spec=%+v)",
				keyTable, gotAPIVersion, pddb2NamespacedDynamoDBAPIVersion, s)
		}
		if strings.HasPrefix(gotAPIVersion, pddb2LegacyDynamoDBGroup+"/") || gotAPIVersion == pddb2LegacyDynamoDBGroup {
			t.Fatalf("desired resource %q uses legacy cluster-scoped group %q; want namespaced group (spec=%+v)",
				keyTable, gotAPIVersion, s)
		}

		// Its Kind must be Table.
		if got := table.Resource.GetKind(); got != "Table" {
			t.Fatalf("desired resource %q Kind = %q; want %q (spec=%+v)", keyTable, got, "Table", s)
		}
	})
}
