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

// pecr2NamespacedECRAPIVersion is the ONLY apiVersion the composed Repository
// may carry: the namespaced (.m.) API group. The legacy cluster-scoped group
// ecr.aws.upbound.io is wrong for a namespaced XR and must never appear.
const pecr2NamespacedECRAPIVersion = "ecr.aws.m.upbound.io/v1beta1"

// pecr2LegacyECRGroup is the legacy cluster-scoped ECR group prefix that must
// never appear on the composed Repository.
const pecr2LegacyECRGroup = "ecr.aws.upbound.io"

// pecr2Environments is the closed set of valid environment values per the XRD
// enum (dev/staging/prod).
var pecr2Environments = []string{"dev", "staging", "prod"}

// pecr2Regions is the XRD region allow-list.
var pecr2Regions = []string{"ap-southeast-1", "us-east-1", "us-west-2", "eu-west-1"}

// pecr2TenantGen produces tenants matching the XRD pattern
// ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$ : a lowercase-alphanumeric first and last
// character with 1–20 characters (lowercase alphanumeric or hyphen) in between.
func pecr2TenantGen() *rapid.Generator[string] {
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

// pecr2Spec captures the drawn TenantEnvironment spec inputs for one iteration.
// repository.enabled is fixed true so a Repository is always emitted; the other
// gated flag (table.enabled) and the S3 fields are varied to check the group and
// Kind hold across the input space.
type pecr2Spec struct {
	tenant       string
	environment  string
	region       string
	versioning   bool
	tableEnabled bool
}

// pecr2GenSpec draws a valid TenantEnvironment spec with repository.enabled
// always true.
func pecr2GenSpec(t *rapid.T) pecr2Spec {
	return pecr2Spec{
		tenant:       pecr2TenantGen().Draw(t, "tenant"),
		environment:  rapid.SampledFrom(pecr2Environments).Draw(t, "environment"),
		region:       rapid.SampledFrom(pecr2Regions).Draw(t, "region"),
		versioning:   rapid.Bool().Draw(t, "versioning"),
		tableEnabled: rapid.Bool().Draw(t, "tableEnabled"),
	}
}

// pecr2BuildRequest constructs a RunFunctionRequest whose observed composite
// resource is a TenantEnvironment carrying the given spec with
// repository.enabled: true.
func pecr2BuildRequest(s pecr2Spec) (*fnv1.RunFunctionRequest, error) {
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
				"enabled": true,
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

// Feature: tenant-environment-ecr, Property 2: The composed Repository uses the namespaced ECR group and correct Kind
//
// For any valid TenantEnvironment spec with repository.enabled: true,
// RunFunction emits a "repository" desired resource carrying apiVersion
// ecr.aws.m.upbound.io/v1beta1 and Kind Repository. The legacy cluster-scoped
// group ecr.aws.upbound.io must never appear on it.
//
// Validates: Requirements 2.4, 6.6
func TestPropertyECR2RepositoryGroupAndKind(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rapid.Check(t, func(t *rapid.T) {
		s := pecr2GenSpec(t)

		req, err := pecr2BuildRequest(s)
		if err != nil {
			t.Fatalf("pecr2BuildRequest(%+v) returned error: %v", s, err)
		}

		rsp, err := f.RunFunction(context.Background(), req)
		if err != nil {
			t.Fatalf("RunFunction(%+v) returned error: %v", s, err)
		}

		dcds, err := request.GetDesiredComposedResources(&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()})
		if err != nil {
			t.Fatalf("GetDesiredComposedResources(%+v) error: %v", s, err)
		}

		// The "repository" entry must exist when enabled.
		repo, ok := dcds[keyRepository]
		if !ok {
			t.Fatalf("desired resources missing %q key with repository.enabled=true (spec=%+v)", keyRepository, s)
		}

		// It must use the namespaced ECR apiVersion, never the legacy group.
		gotAPIVersion := repo.Resource.GetAPIVersion()
		if gotAPIVersion != pecr2NamespacedECRAPIVersion {
			t.Fatalf("desired resource %q apiVersion = %q; want %q (spec=%+v)",
				keyRepository, gotAPIVersion, pecr2NamespacedECRAPIVersion, s)
		}
		if strings.HasPrefix(gotAPIVersion, pecr2LegacyECRGroup+"/") || gotAPIVersion == pecr2LegacyECRGroup {
			t.Fatalf("desired resource %q uses legacy cluster-scoped group %q; want namespaced group (spec=%+v)",
				keyRepository, gotAPIVersion, s)
		}

		// Its Kind must be Repository.
		if got := repo.Resource.GetKind(); got != "Repository" {
			t.Fatalf("desired resource %q Kind = %q; want %q (spec=%+v)", keyRepository, got, "Repository", s)
		}
	})
}
