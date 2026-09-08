// Package main contains the embedded composition function for
// TenantEnvironment. This file holds RunFunction — the composition entrypoint —
// and the serve wiring. The pure naming/validation logic lives in naming.go,
// which imports no Crossplane packages.
package main

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/fieldpath"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"

	metav1 "dev.crossplane.io/models/io/k8s/meta/v1"
	s3v1beta1 "dev.crossplane.io/models/io/upbound/m/aws/s3/v1beta1"
)

// externalNameAnnotation is the annotation key that carries the real AWS
// resource name. The AWS bucket name is set here, never via metadata.name.
const externalNameAnnotation = "crossplane.io/external-name"

// ptr returns a pointer to v. The generated models use pointer fields
// throughout, so building them requires taking addresses of literals.
func ptr[T any](v T) *T { return &v }

// Composition-resource map keys. These are stable identifiers by contract —
// renaming them orphans live AWS resources, so they are fixed. The Bucket is
// added under keyBucket in task 6.2 and the versioning under keyBucketVersioning
// in task 7.1.
const (
	keyBucket           resource.Name = "bucket"
	keyBucketVersioning resource.Name = "bucket-versioning"
)

// defaultRegion is the XRD default for spec.region. The XRD normally populates
// the observed XR with this value; the function tolerates an empty value
// defensively.
const defaultRegion = "ap-southeast-1"

// Function implements the composition function's gRPC RunFunctionService.
type Function struct {
	fnv1.UnimplementedFunctionRunnerServiceServer

	log logging.Logger
}

// RunFunction turns an observed TenantEnvironment XR into the desired S3
// managed resources. It reads the observed XR, derives the deterministic names,
// assembles the desired resources into a map keyed by their stable
// composition-resource names, and — in a single deferred block — converts that
// map into the SDK's desired-composed representation and writes it back onto the
// response. Emitting a resource is therefore just a matter of adding it to
// desired; conversion happens once, in one place.
func (f *Function) RunFunction(_ context.Context, req *fnv1.RunFunctionRequest) (*fnv1.RunFunctionResponse, error) {
	f.log.Info("Running function", "tag", req.GetMeta().GetTag())

	rsp := response.To(req, response.DefaultTTL)

	// Read the observed composite resource (the TenantEnvironment XR). If it
	// cannot be read, surface a fatal result on the XR and emit nothing (R7.1).
	oxr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot get observed composite resource"))
		return rsp, nil
	}

	// The desired resources map. Entries are added below and converted to SDK
	// desired-composed resources in the deferred block. In this task the map is
	// empty; the Bucket (task 6.2) and BucketVersioning (task 7.1) populate it.
	desired := map[resource.Name]any{}

	// Convert the assembled desired map into the SDK representation and write it
	// back to the response exactly once, regardless of which return path is
	// taken below. Structured after the scaffold's deferred-conversion pattern —
	// resources are collected as typed models, then converted in one place.
	//
	// A fatal result means unrecoverable input: the function must emit zero
	// desired managed resources (R7.1, Property 11). Every fatal path returns
	// before adding anything to desired, so the map is already empty there — but
	// guard explicitly so the invariant does not rely on ordering, and so a
	// fatal set inside this deferred block itself never emits a partial set.
	// response.To seeds rsp.Desired from the request; skipping the merge on a
	// fatal result keeps the function from contributing any resource.
	defer func() {
		if hasFatalResult(rsp) {
			return
		}
		dcds, cerr := toDesiredComposed(desired)
		if cerr != nil {
			response.Fatal(rsp, errors.Wrap(cerr, "cannot build desired composed resources"))
			return
		}
		if serr := response.SetDesiredComposedResources(rsp, dcds); serr != nil {
			response.Fatal(rsp, errors.Wrap(serr, "cannot set desired composed resources"))
			return
		}
	}()

	// Read the scalar spec fields off the observed XR. The SDK exposes the XR as
	// an unstructured composite; read scalar spec fields via fieldpath.
	paved := fieldpath.Pave(oxr.Resource.UnstructuredContent())

	tenant, err := paved.GetString("spec.tenant")
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot read spec.tenant from observed XR"))
		return rsp, nil
	}

	environment, err := paved.GetString("spec.environment")
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot read spec.environment from observed XR"))
		return rsp, nil
	}

	// Derive the deterministic names. On error (empty/whitespace tenant or
	// environment) surface a fatal result and emit nothing (R2.7, R3.6, R7.1).
	names, err := BuildNames(tenant, environment)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot derive bucket external name"))
		return rsp, nil
	}

	// Resolve region. The XRD default means it is normally populated; tolerate
	// an absent value defensively by falling back to the default.
	region, err := paved.GetString("spec.region")
	if err != nil {
		if !fieldpath.IsNotFound(err) {
			response.Fatal(rsp, errors.Wrap(err, "cannot read spec.region from observed XR"))
			return rsp, nil
		}
		region = defaultRegion
	}

	// Resolve versioning. Absent means true (the schema default) → Enabled.
	versioning := true
	if v, verr := paved.GetBool("spec.bucket.versioning"); verr != nil {
		if !fieldpath.IsNotFound(verr) {
			response.Fatal(rsp, errors.Wrap(verr, "cannot read spec.bucket.versioning from observed XR"))
			return rsp, nil
		}
	} else {
		versioning = v
	}

	// Build the S3 Bucket unconditionally for every TenantEnvironment,
	// independent of spec.table.enabled / spec.repository.enabled (R2.1, R2.2).
	// The AWS name is carried by the crossplane.io/external-name annotation
	// (<tenant>-<env>-bucket); metadata.name is deliberately left unset so it is
	// never derived from tenant/environment (R2.6, R3.3, R3.4). The resource is
	// placed in the XR's namespace <tenant>-<env> (R2.4) and uses the namespaced
	// .m. API group (R2.3, R6.4).
	tags := StandardTags(tenant, environment)
	desired[keyBucket] = &s3v1beta1.Bucket{
		APIVersion: ptr(s3v1beta1.BucketAPIVersionS3AwsMUpboundIoV1Beta1),
		Kind:       ptr(s3v1beta1.BucketKindBucket),
		Metadata: &metav1.ObjectMeta{
			Namespace: ptr(names.Namespace),
			Annotations: ptr(map[string]string{
				externalNameAnnotation: names.BucketName,
			}),
		},
		Spec: &s3v1beta1.BucketSpec{
			ForProvider: &s3v1beta1.BucketSpecForProvider{
				Region: ptr(region),
				Tags:   ptr(tags),
			},
		},
	}

	// Build the BucketVersioning managed resource under keyBucketVersioning.
	// Guard (R4.8): the versioning resource references the Bucket by its
	// external name, so it must never be emitted without the bucket present. In
	// this slice the bucket is unconditional, so this only fires on an internal
	// inconsistency; when it does, surface a warning identifying the missing
	// bucket and skip the versioning resource rather than emitting a dangling
	// reference.
	if _, ok := desired[keyBucket]; !ok {
		response.Warning(rsp, errors.Errorf("cannot emit %q: referenced bucket %q is missing from desired resources", keyBucketVersioning, keyBucket))
		return rsp, nil
	}

	// versioningConfiguration.status is the only value set — Enabled|Suspended
	// mapped from spec.bucket.versioning via VersioningStatus (R4.3, R4.4,
	// R4.5). The resource shares the bucket's namespace (R4.6) and references
	// the bucket by its external name <tenant>-<env>-bucket (R4.7), using the
	// namespaced .m. API group (R4.2, R6.4). BucketVersioning carries no tags
	// field, so the standard tag set applies only to the Bucket (R6.5).
	desired[keyBucketVersioning] = &s3v1beta1.BucketVersioning{
		APIVersion: ptr(s3v1beta1.BucketVersioningAPIVersionS3AwsMUpboundIoV1Beta1),
		Kind:       ptr(s3v1beta1.BucketVersioningKindBucketVersioning),
		Metadata: &metav1.ObjectMeta{
			Namespace: ptr(names.Namespace),
		},
		Spec: &s3v1beta1.BucketVersioningSpec{
			ForProvider: &s3v1beta1.BucketVersioningSpecForProvider{
				Bucket: ptr(names.BucketName),
				Region: ptr(region),
				VersioningConfiguration: &s3v1beta1.BucketVersioningSpecForProviderVersioningConfiguration{
					Status: ptr(VersioningStatus(versioning)),
				},
			},
		},
	}

	// Populate status.bucketName from the CURRENT observed bucket readiness
	// (R5). status is derived fresh from the observed composed bucket's Ready
	// condition on every reconcile — it is never latched — so a ready→not-ready
	// transition naturally clears it (R5.3). status.tableName and
	// status.repositoryUrl are left unset in this slice (R5.5, R5.6).
	if err := f.populateStatus(req, rsp, names); err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot populate status"))
		return rsp, nil
	}

	return rsp, nil
}

// populateStatus sets status.bucketName on the desired composite resource when
// the observed composed bucket reports Ready, and leaves it unset otherwise.
//
// The desired composite is initialised from the observed composite, so reading
// it back and setting only status.bucketName preserves the rest of the XR. When
// the bucket is not Ready the field is left untouched, which — because the
// desired composite starts from the observed one each reconcile and the value
// is re-derived every time rather than latched — means status reflects only a
// currently-ready bucket (R5.1, R5.2, R5.3, R5.4).
func (f *Function) populateStatus(req *fnv1.RunFunctionRequest, rsp *fnv1.RunFunctionResponse, names Names) error {
	observed, err := request.GetObservedComposedResources(req)
	if err != nil {
		return errors.Wrap(err, "cannot get observed composed resources")
	}

	// The bucket may not be observed yet (first reconcile, before the provider
	// has reported anything back). Absent or not-Ready both mean "leave
	// status.bucketName unset".
	bucket, ok := observed[keyBucket]
	if !ok {
		return nil
	}
	if bucket.Resource.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
		return nil
	}

	// The bucket is Ready: set status.bucketName to the bucket external name,
	// byte-for-byte, on the desired composite (R5.1, R5.4).
	dxr, err := request.GetDesiredCompositeResource(req)
	if err != nil {
		return errors.Wrap(err, "cannot get desired composite resource")
	}
	if err := fieldpath.Pave(dxr.Resource.Object).SetString("status.bucketName", names.BucketName); err != nil {
		return errors.Wrap(err, "cannot set status.bucketName on desired composite resource")
	}
	if err := response.SetDesiredCompositeResource(rsp, dxr); err != nil {
		return errors.Wrap(err, "cannot set desired composite resource")
	}
	return nil
}

// toDesiredComposed converts the assembled map of typed managed-resource models
// into the SDK's desired-composed representation. Each value in desired is a
// typed generated model (e.g. *s3v1beta1.Bucket); it is marshalled onto a fresh
// DesiredComposed resource. Keeping this conversion in one place is the
// scaffold's deferred-conversion pattern — callers only ever add typed models to
// the map.
func toDesiredComposed(desired map[resource.Name]any) (map[resource.Name]*resource.DesiredComposed, error) {
	dcds := make(map[resource.Name]*resource.DesiredComposed, len(desired))
	for name, obj := range desired {
		// The generated models are plain oapi-codegen structs (not
		// runtime.Object), so convert them to unstructured content via a JSON
		// round-trip and wrap in a composed.Unstructured.
		content, err := toUnstructuredContent(obj)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot convert desired resource %q", name)
		}
		dcds[name] = &resource.DesiredComposed{
			Resource: &composed.Unstructured{Unstructured: unstructured.Unstructured{Object: content}},
		}
	}
	return dcds, nil
}

// hasFatalResult reports whether the response already carries a fatal result.
// It is used to short-circuit the deferred desired-resource conversion so that
// a fatal, unrecoverable input yields zero desired managed resources from this
// function (R7.1, Property 11), independent of the order in which resources
// were assembled.
func hasFatalResult(rsp *fnv1.RunFunctionResponse) bool {
	for _, r := range rsp.GetResults() {
		if r.GetSeverity() == fnv1.Severity_SEVERITY_FATAL {
			return true
		}
	}
	return false
}

// toUnstructuredContent marshals a typed generated model to JSON and back into a
// map[string]any suitable for a composed.Unstructured.
func toUnstructuredContent(obj any) (map[string]any, error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, errors.Wrap(err, "cannot marshal to JSON")
	}
	content := map[string]any{}
	if err := json.Unmarshal(b, &content); err != nil {
		return nil, errors.Wrap(err, "cannot unmarshal JSON into unstructured content")
	}
	return content, nil
}
