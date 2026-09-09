package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
)

// fntSpec captures the TenantEnvironment spec inputs a table case feeds into a
// synthesised observed XR. A nil *bool for versioning models the field being
// absent from the spec entirely (schema default → true → Enabled); a non-nil
// value sets bucket.versioning explicitly. The tenant/environment values mirror
// the example manifests under examples/tenantenvironments/.
type fntSpec struct {
	tenant       string
	environment  string
	region       string // "" → omit spec.region (rely on XRD/function default)
	versioning   *bool  // nil → omit spec.bucket.versioning
	tableEnabled bool
	tableHashKey string // "" → omit spec.table.hashKey (rely on function default "id")
	billingMode  string // "" → omit spec.table.billingMode (rely on default PAY_PER_REQUEST)
	repoEnabled  bool
	// omitTenant, when true, drops spec.tenant from the XR entirely to model a
	// malformed input. omitEnvironment does the same for spec.environment.
	omitTenant      bool
	omitEnvironment bool
	emptyTenant     bool // set spec.tenant to "" (present but empty)
}

// fntBucketWant is the observable slice of a desired Bucket resource asserted by
// a table case: the fields task 9.3 pins down. Comparing this projection with
// go-cmp keeps the assertion readable and resilient to unrelated future fields.
type fntBucketWant struct {
	APIVersion   string
	Kind         string
	Namespace    string
	ExternalName string
	Region       string
	Tags         map[string]string
}

// fntVersioningWant is the observable slice of a desired BucketVersioning
// resource asserted by a table case.
type fntVersioningWant struct {
	APIVersion  string
	Kind        string
	Namespace   string
	Bucket      string
	StatusValue string
}

// fntRepositoryWant is the observable slice of a desired Repository (ECR)
// resource asserted by a table case: apiVersion/Kind, namespace, external name,
// region, and the three standard tags.
type fntRepositoryWant struct {
	APIVersion   string
	Kind         string
	Namespace    string
	ExternalName string
	Region       string
	Tags         map[string]string
}

// fntAttributeWant is one entry in a Table's spec.forProvider.attribute list,
// projected to the name/type pair the key-schema assertions care about.
type fntAttributeWant struct {
	Name string
	Type string
}

// fntTableWant is the observable slice of a desired Table (DynamoDB) resource
// asserted by a table case: apiVersion/Kind, namespace, external name, region,
// the three standard tags, the hash key, the billing mode, the single attribute
// definition, and the provisioned-mode read/write capacities. HasName records
// whether metadata.name is present, so a case can assert it is never set to a
// tenant/env-derived value. ReadCapacity/WriteCapacity are pointers so a case
// distinguishes "unset" (PAY_PER_REQUEST) from an explicit 1 (PROVISIONED).
type fntTableWant struct {
	APIVersion    string
	Kind          string
	Namespace     string
	ExternalName  string
	Region        string
	Tags          map[string]string
	HashKey       string
	BillingMode   string
	Attributes    []fntAttributeWant
	ReadCapacity  *float64
	WriteCapacity *float64
	HasName       bool
}

// fntBuildRequest synthesises a RunFunctionRequest whose observed composite is a
// TenantEnvironment carrying the given spec. Absent fields are genuinely omitted
// so the function's default/guard paths are exercised, mirroring how the render
// pipeline presents a defaulted XR.
func fntBuildRequest(t *testing.T, s fntSpec) *fnv1.RunFunctionRequest {
	t.Helper()

	spec := map[string]any{}
	if !s.omitTenant {
		if s.emptyTenant {
			spec["tenant"] = ""
		} else {
			spec["tenant"] = s.tenant
		}
	}
	if !s.omitEnvironment {
		spec["environment"] = s.environment
	}
	if s.region != "" {
		spec["region"] = s.region
	}
	if s.versioning != nil {
		spec["bucket"] = map[string]any{"versioning": *s.versioning}
	}
	table := map[string]any{"enabled": s.tableEnabled}
	if s.tableHashKey != "" {
		table["hashKey"] = s.tableHashKey
	}
	if s.billingMode != "" {
		table["billingMode"] = s.billingMode
	}
	spec["table"] = table
	spec["repository"] = map[string]any{"enabled": s.repoEnabled}

	// The XR namespace/name are <tenant>-<env> per the design invariant; for
	// malformed cases the exact metadata is irrelevant to the assertions.
	ns := s.tenant + "-" + s.environment
	xr := map[string]any{
		"apiVersion": "platform.hello-crossplane.io/v1alpha1",
		"kind":       "TenantEnvironment",
		"metadata": map[string]any{
			"name":      ns,
			"namespace": ns,
		},
		"spec": spec,
	}

	xrStruct, err := structpb.NewStruct(xr)
	if err != nil {
		t.Fatalf("fntBuildRequest: cannot build structpb for XR: %v", err)
	}

	return &fnv1.RunFunctionRequest{
		Observed: &fnv1.State{
			Composite: &fnv1.Resource{Resource: xrStruct},
		},
	}
}

// fntGetString reads a dotted string path off an unstructured resource content
// map, returning ("", false) when any segment along the path is absent.
func fntGetString(content map[string]any, path ...string) (string, bool) {
	cur := any(content)
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[seg]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// fntGetStringMap reads a dotted path expected to hold a map[string]string
// (JSON object of strings), returning nil when absent.
func fntGetStringMap(content map[string]any, path ...string) map[string]string {
	cur := any(content)
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// fntProjectBucket extracts the observable Bucket slice from a desired composed
// resource's unstructured content.
func fntProjectBucket(content map[string]any) fntBucketWant {
	apiVersion, _ := fntGetString(content, "apiVersion")
	kind, _ := fntGetString(content, "kind")
	ns, _ := fntGetString(content, "metadata", "namespace")
	extName, _ := fntGetString(content, "metadata", "annotations", externalNameAnnotation)
	region, _ := fntGetString(content, "spec", "forProvider", "region")
	tags := fntGetStringMap(content, "spec", "forProvider", "tags")
	return fntBucketWant{
		APIVersion:   apiVersion,
		Kind:         kind,
		Namespace:    ns,
		ExternalName: extName,
		Region:       region,
		Tags:         tags,
	}
}

// fntProjectVersioning extracts the observable BucketVersioning slice from a
// desired composed resource's unstructured content.
func fntProjectVersioning(content map[string]any) fntVersioningWant {
	apiVersion, _ := fntGetString(content, "apiVersion")
	kind, _ := fntGetString(content, "kind")
	ns, _ := fntGetString(content, "metadata", "namespace")
	bucket, _ := fntGetString(content, "spec", "forProvider", "bucket")
	status, _ := fntGetString(content, "spec", "forProvider", "versioningConfiguration", "status")
	return fntVersioningWant{
		APIVersion:  apiVersion,
		Kind:        kind,
		Namespace:   ns,
		Bucket:      bucket,
		StatusValue: status,
	}
}

// fntProjectRepository extracts the observable Repository slice from a desired
// composed resource's unstructured content.
func fntProjectRepository(content map[string]any) fntRepositoryWant {
	apiVersion, _ := fntGetString(content, "apiVersion")
	kind, _ := fntGetString(content, "kind")
	ns, _ := fntGetString(content, "metadata", "namespace")
	extName, _ := fntGetString(content, "metadata", "annotations", externalNameAnnotation)
	region, _ := fntGetString(content, "spec", "forProvider", "region")
	tags := fntGetStringMap(content, "spec", "forProvider", "tags")
	return fntRepositoryWant{
		APIVersion:   apiVersion,
		Kind:         kind,
		Namespace:    ns,
		ExternalName: extName,
		Region:       region,
		Tags:         tags,
	}
}

// fntGetNumber reads a dotted path expected to hold a JSON number, returning
// (0, false) when any segment is absent or the value is not numeric. Numbers in
// unstructured content round-trip through JSON as float64.
func fntGetNumber(content map[string]any, path ...string) (float64, bool) {
	cur := any(content)
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = m[seg]
		if !ok {
			return 0, false
		}
	}
	f, ok := cur.(float64)
	return f, ok
}

// fntGetAttributes reads spec.forProvider.attribute as a slice of {name, type}
// pairs, returning nil when absent.
func fntGetAttributes(content map[string]any) []fntAttributeWant {
	cur := any(content)
	for _, seg := range []string{"spec", "forProvider", "attribute"} {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	list, ok := cur.([]any)
	if !ok {
		return nil
	}
	out := make([]fntAttributeWant, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		typ, _ := m["type"].(string)
		out = append(out, fntAttributeWant{Name: name, Type: typ})
	}
	return out
}

// fntProjectTable extracts the observable Table slice from a desired composed
// resource's unstructured content. Read/write capacities are projected as
// pointers so an unset value (PAY_PER_REQUEST) is distinguishable from an
// explicit 1 (PROVISIONED).
func fntProjectTable(content map[string]any) fntTableWant {
	apiVersion, _ := fntGetString(content, "apiVersion")
	kind, _ := fntGetString(content, "kind")
	ns, _ := fntGetString(content, "metadata", "namespace")
	extName, _ := fntGetString(content, "metadata", "annotations", externalNameAnnotation)
	region, _ := fntGetString(content, "spec", "forProvider", "region")
	tags := fntGetStringMap(content, "spec", "forProvider", "tags")
	hashKey, _ := fntGetString(content, "spec", "forProvider", "hashKey")
	billingMode, _ := fntGetString(content, "spec", "forProvider", "billingMode")
	_, hasName := fntGetString(content, "metadata", "name")

	var readCap, writeCap *float64
	if v, ok := fntGetNumber(content, "spec", "forProvider", "readCapacity"); ok {
		readCap = &v
	}
	if v, ok := fntGetNumber(content, "spec", "forProvider", "writeCapacity"); ok {
		writeCap = &v
	}

	return fntTableWant{
		APIVersion:    apiVersion,
		Kind:          kind,
		Namespace:     ns,
		ExternalName:  extName,
		Region:        region,
		Tags:          tags,
		HashKey:       hashKey,
		BillingMode:   billingMode,
		Attributes:    fntGetAttributes(content),
		ReadCapacity:  readCap,
		WriteCapacity: writeCap,
		HasName:       hasName,
	}
}

// TestRunFunction is the table-driven wiring/edge-case suite for RunFunction. It
// covers the two example XRs (acme-dev, globex-prod), a malformed XR that must
// yield a fatal result and zero desired resources, a crafted PROVISIONED table
// case, and — as far as the public RunFunction allows — the R4.8 versioning
// guard.
//
// _Requirements: 2.1, 2.5, 2.6, 2.8, 4.1, 4.3, 4.4, 4.5, 4.6, 4.7, 5.1, 7.1, 7.2, 7.4, 7.6, 4.8_
func TestRunFunction(t *testing.T) {
	bTrue := true

	type want struct {
		fatal         bool               // response carries a SEVERITY_FATAL result
		resourceCount int                // exact number of desired composed resources
		bucket        *fntBucketWant     // nil → do not assert the bucket projection
		versioning    *fntVersioningWant // nil → do not assert the versioning projection
		repository    *fntRepositoryWant // nil → assert the "repository" key is ABSENT
		table         *fntTableWant      // nil → assert the "table" key is ABSENT
	}

	cases := map[string]struct {
		spec fntSpec
		want want
	}{
		// examples/tenantenvironments/acme-dev.yaml: tenant=acme, environment=dev,
		// no region (function default ap-southeast-1), no bucket.versioning
		// (schema default → Enabled), table & repo disabled.
		"acme-dev": {
			spec: fntSpec{
				tenant:       "acme",
				environment:  "dev",
				tableEnabled: false,
				repoEnabled:  false,
			},
			want: want{
				resourceCount: 2,
				bucket: &fntBucketWant{
					APIVersion:   "s3.aws.m.upbound.io/v1beta1",
					Kind:         "Bucket",
					Namespace:    "acme-dev",
					ExternalName: "acme-dev-bucket",
					Region:       defaultRegion,
					Tags: map[string]string{
						"tenant":      "acme",
						"environment": "dev",
						"managed-by":  "crossplane",
					},
				},
				versioning: &fntVersioningWant{
					APIVersion:  "s3.aws.m.upbound.io/v1beta1",
					Kind:        "BucketVersioning",
					Namespace:   "acme-dev",
					Bucket:      "acme-dev-bucket",
					StatusValue: "Enabled",
				},
			},
		},

		// examples/tenantenvironments/globex-prod.yaml: tenant=globex,
		// environment=prod, region ap-southeast-1, bucket.versioning explicitly
		// true → Enabled, table & repo enabled (hashKey id, billingMode
		// PAY_PER_REQUEST). repository.enabled: true and table.enabled: true each
		// add a resource, so the keyset is
		// {bucket, bucket-versioning, repository, table} (R7.6).
		"globex-prod": {
			spec: fntSpec{
				tenant:       "globex",
				environment:  "prod",
				region:       "ap-southeast-1",
				versioning:   &bTrue,
				tableEnabled: true,
				repoEnabled:  true,
			},
			want: want{
				resourceCount: 4,
				bucket: &fntBucketWant{
					APIVersion:   "s3.aws.m.upbound.io/v1beta1",
					Kind:         "Bucket",
					Namespace:    "globex-prod",
					ExternalName: "globex-prod-bucket",
					Region:       "ap-southeast-1",
					Tags: map[string]string{
						"tenant":      "globex",
						"environment": "prod",
						"managed-by":  "crossplane",
					},
				},
				versioning: &fntVersioningWant{
					APIVersion:  "s3.aws.m.upbound.io/v1beta1",
					Kind:        "BucketVersioning",
					Namespace:   "globex-prod",
					Bucket:      "globex-prod-bucket",
					StatusValue: "Enabled",
				},
				repository: &fntRepositoryWant{
					APIVersion:   "ecr.aws.m.upbound.io/v1beta1",
					Kind:         "Repository",
					Namespace:    "globex-prod",
					ExternalName: "globex-prod-ecr",
					Region:       "ap-southeast-1",
					Tags: map[string]string{
						"tenant":      "globex",
						"environment": "prod",
						"managed-by":  "crossplane",
					},
				},
				// Table enabled with the schema defaults: hashKey id, one
				// attribute {id, S}, billingMode PAY_PER_REQUEST so no
				// read/write capacities are set (R2.1, R2.5, R2.6, R2.8, R4.1,
				// R4.3, R4.4, R4.6, R5.1). metadata.name must never be set.
				table: &fntTableWant{
					APIVersion:   "dynamodb.aws.m.upbound.io/v1beta1",
					Kind:         "Table",
					Namespace:    "globex-prod",
					ExternalName: "globex-prod-dtbl",
					Region:       "ap-southeast-1",
					Tags: map[string]string{
						"tenant":      "globex",
						"environment": "prod",
						"managed-by":  "crossplane",
					},
					HashKey:       "id",
					BillingMode:   "PAY_PER_REQUEST",
					Attributes:    []fntAttributeWant{{Name: "id", Type: "S"}},
					ReadCapacity:  nil,
					WriteCapacity: nil,
					HasName:       false,
				},
			},
		},

		// Crafted PROVISIONED table case (not from an example file): a table
		// enabled with billingMode PROVISIONED must carry readCapacity and
		// writeCapacity each equal to 1 (R4.7). Repository is disabled, so the
		// keyset is {bucket, bucket-versioning, table}.
		"provisioned table sets read and write capacity to one": {
			spec: fntSpec{
				tenant:       "acme",
				environment:  "staging",
				tableEnabled: true,
				tableHashKey: "pk",
				billingMode:  "PROVISIONED",
				repoEnabled:  false,
			},
			want: want{
				resourceCount: 3,
				table: &fntTableWant{
					APIVersion:   "dynamodb.aws.m.upbound.io/v1beta1",
					Kind:         "Table",
					Namespace:    "acme-staging",
					ExternalName: "acme-staging-dtbl",
					Region:       defaultRegion,
					Tags: map[string]string{
						"tenant":      "acme",
						"environment": "staging",
						"managed-by":  "crossplane",
					},
					HashKey:       "pk",
					BillingMode:   "PROVISIONED",
					Attributes:    []fntAttributeWant{{Name: "pk", Type: "S"}},
					ReadCapacity:  ptr(float64(1)),
					WriteCapacity: ptr(float64(1)),
					HasName:       false,
				},
			},
		},

		// Malformed XR: spec.tenant present but empty. BuildNames rejects it, so
		// the function surfaces a fatal result on the composite and emits zero
		// desired resources (R7.1).
		"malformed empty tenant yields fatal and no resources": {
			spec: fntSpec{
				emptyTenant: true,
				environment: "dev",
			},
			want: want{
				fatal:         true,
				resourceCount: 0,
			},
		},

		// Malformed XR: spec.tenant omitted entirely → reading spec.tenant fails
		// → fatal, zero resources (R7.1).
		"malformed missing tenant yields fatal and no resources": {
			spec: fntSpec{
				omitTenant:  true,
				environment: "dev",
			},
			want: want{
				fatal:         true,
				resourceCount: 0,
			},
		},
	}

	fn := &Function{log: logging.NewNopLogger()}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := fntBuildRequest(t, tc.spec)

			rsp, err := fn.RunFunction(context.Background(), req)
			if err != nil {
				t.Fatalf("RunFunction returned transport error: %v", err)
			}

			// Fatal assertion: a fatal case must carry a SEVERITY_FATAL result,
			// and a non-fatal case must not.
			if got := fntHasFatal(rsp); got != tc.want.fatal {
				t.Fatalf("fatal result presence = %v; want %v (results: %+v)",
					got, tc.want.fatal, rsp.GetResults())
			}

			// Read desired composed resources via the request helper — the v0.7.1
			// SDK exposes them through request.GetDesiredComposedResources, not a
			// response.* getter.
			desired, err := request.GetDesiredComposedResources(
				&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()},
			)
			if err != nil {
				t.Fatalf("cannot read desired composed resources: %v", err)
			}

			if len(desired) != tc.want.resourceCount {
				keys := make([]string, 0, len(desired))
				for k := range desired {
					keys = append(keys, string(k))
				}
				t.Fatalf("desired composed resource count = %d; want %d (keys: %v)",
					len(desired), tc.want.resourceCount, keys)
			}

			if tc.want.bucket != nil {
				b, ok := desired[keyBucket]
				if !ok {
					t.Fatalf("desired resources missing key %q", keyBucket)
				}
				got := fntProjectBucket(b.Resource.UnstructuredContent())
				if diff := cmp.Diff(*tc.want.bucket, got); diff != "" {
					t.Errorf("bucket projection mismatch (-want +got):\n%s", diff)
				}
			}

			if tc.want.versioning != nil {
				v, ok := desired[keyBucketVersioning]
				if !ok {
					t.Fatalf("desired resources missing key %q", keyBucketVersioning)
				}
				got := fntProjectVersioning(v.Resource.UnstructuredContent())
				if diff := cmp.Diff(*tc.want.versioning, got); diff != "" {
					t.Errorf("versioning projection mismatch (-want +got):\n%s", diff)
				}

				// R4.7 structural coverage: versioning always references the
				// bucket that is present in the same response. This is the
				// observable half of the R4.8 guard — the guard withholds
				// versioning when the bucket is absent, so when the bucket IS
				// present, versioning must reference it by external name.
				if tc.want.bucket != nil && got.Bucket != tc.want.bucket.ExternalName {
					t.Errorf("versioning references bucket %q; want the composed bucket external name %q",
						got.Bucket, tc.want.bucket.ExternalName)
				}
			}

			// Repository (ECR) is optional and gated by spec.repository.enabled.
			// A non-nil want.repository asserts the projection; a nil one asserts
			// the "repository" key is absent entirely (the disabled path must not
			// emit the resource at all).
			if tc.want.repository != nil {
				r, ok := desired[keyRepository]
				if !ok {
					t.Fatalf("desired resources missing key %q", keyRepository)
				}
				got := fntProjectRepository(r.Resource.UnstructuredContent())
				if diff := cmp.Diff(*tc.want.repository, got); diff != "" {
					t.Errorf("repository projection mismatch (-want +got):\n%s", diff)
				}
			} else if _, ok := desired[keyRepository]; ok {
				t.Errorf("desired resources contain key %q; want it absent (repository disabled)", keyRepository)
			}

			// Table (DynamoDB) is optional and gated by spec.table.enabled. A
			// non-nil want.table asserts the projection; a nil one asserts the
			// "table" key is absent entirely (the disabled path must not emit
			// the resource at all).
			if tc.want.table != nil {
				tbl, ok := desired[keyTable]
				if !ok {
					t.Fatalf("desired resources missing key %q", keyTable)
				}
				got := fntProjectTable(tbl.Resource.UnstructuredContent())
				if diff := cmp.Diff(*tc.want.table, got); diff != "" {
					t.Errorf("table projection mismatch (-want +got):\n%s", diff)
				}
				// metadata.name must never be set to the external name or any
				// tenant/env-derived value (R2.8, R3.4).
				if got.HasName {
					t.Errorf("table metadata.name is set; want it absent so the AWS name comes only from the external-name annotation")
				}
			} else if _, ok := desired[keyTable]; ok {
				t.Errorf("desired resources contain key %q; want it absent (table disabled)", keyTable)
			}

			// status.tableName must be absent whenever no observed Table reports
			// Ready — which is every case here, since none synthesise observed
			// composed resources (R6.2, R6.5). Assert absence on the desired
			// composite the function wrote back.
			if !tc.want.fatal {
				if _, present := fntStatusTableName(rsp); present {
					t.Errorf("status.tableName is set; want it absent (no observed Table reports Ready)")
				}
			}
		})
	}
}

// fntStatusTableName reads status.tableName off the desired composite resource
// carried in the response, returning ("", false) when the composite or the
// field is absent. The function writes status onto the desired composite via
// response.SetDesiredCompositeResource, so this reads rsp.Desired.Composite.
func fntStatusTableName(rsp *fnv1.RunFunctionResponse) (string, bool) {
	comp := rsp.GetDesired().GetComposite()
	if comp == nil {
		return "", false
	}
	return fntGetString(comp.GetResource().AsMap(), "status", "tableName")
}

// fntHasFatal reports whether the response carries a SEVERITY_FATAL result. The
// production hasFatalResult helper is unexported in the same package, but this
// test-local reader keeps the assertion self-contained and independent of that
// helper's continued existence.
func fntHasFatal(rsp *fnv1.RunFunctionResponse) bool {
	for _, r := range rsp.GetResults() {
		if r.GetSeverity() == fnv1.Severity_SEVERITY_FATAL {
			return true
		}
	}
	return false
}

// TestRunFunctionR48GuardIsDefensive documents the R4.8 guard's testability
// boundary. The guard in fn.go withholds bucket-versioning and emits a warning
// when the "bucket" desired entry is absent while assembling versioning. Because
// the public RunFunction always adds the bucket unconditionally before assembling
// versioning, that branch cannot be reached through RunFunction without modifying
// fn.go to inject the inconsistent internal state — which task 9.3 explicitly
// forbids ("Do NOT modify fn.go to expose internals").
//
// So this case covers the guard structurally rather than by triggering it: it
// asserts the observable contract the guard protects — that whenever a bucket is
// composed, bucket-versioning references that same bucket by external name and
// shares its namespace. If a future refactor made the bucket conditional, the
// guard's negative path (no bucket → no versioning + warning) would become
// reachable and warrant a direct case here.
//
// _Requirements: 4.8_
func TestRunFunctionR48GuardIsDefensive(t *testing.T) {
	fn := &Function{log: logging.NewNopLogger()}

	req := fntBuildRequest(t, fntSpec{
		tenant:      "acme",
		environment: "dev",
	})

	rsp, err := fn.RunFunction(context.Background(), req)
	if err != nil {
		t.Fatalf("RunFunction returned transport error: %v", err)
	}

	desired, err := request.GetDesiredComposedResources(
		&fnv1.RunFunctionRequest{Desired: rsp.GetDesired()},
	)
	if err != nil {
		t.Fatalf("cannot read desired composed resources: %v", err)
	}

	bucket, hasBucket := desired[keyBucket]
	versioning, hasVersioning := desired[keyBucketVersioning]

	// The guard's invariant: versioning is only ever emitted alongside the
	// bucket it references. Here both are present; assert the reference and
	// namespace consistency the guard exists to protect.
	if !hasBucket {
		t.Fatalf("expected the bucket to be composed unconditionally")
	}
	if !hasVersioning {
		t.Fatalf("expected bucket-versioning alongside the composed bucket")
	}

	b := fntProjectBucket(bucket.Resource.UnstructuredContent())
	v := fntProjectVersioning(versioning.Resource.UnstructuredContent())

	if v.Bucket != b.ExternalName {
		t.Errorf("versioning references bucket %q; want the composed bucket external name %q",
			v.Bucket, b.ExternalName)
	}
	if v.Namespace != b.Namespace {
		t.Errorf("versioning namespace %q; want the bucket namespace %q", v.Namespace, b.Namespace)
	}
}
