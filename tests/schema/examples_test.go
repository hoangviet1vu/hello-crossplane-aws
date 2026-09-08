package schema

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"sigs.k8s.io/yaml"
)

// examplesRelDir is the directory, relative to the repository root, holding the
// example TenantEnvironment custom resources under test.
const examplesRelDir = "examples/tenantenvironments"

// exampleTenantPattern is the tenant name rule the example names must satisfy
// (Requirement 10.3). It mirrors the pattern in definition.yaml.
var exampleTenantPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`)

// exampleEnvironments is the set of permitted spec.environment values
// (Requirement 10.3).
var exampleEnvironments = map[string]struct{}{
	"dev":     {},
	"staging": {},
	"prod":    {},
}

// exampleFile pairs a parsed example document with its file name for
// diagnostics.
type exampleFile struct {
	name string
	doc  Document
}

// enumerateExamples resolves examples/tenantenvironments/ via RepoRoot, reads
// every *.yaml file in it, parses each into a Document, and returns them sorted
// by file name for deterministic iteration.
func enumerateExamples(t *testing.T) []exampleFile {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	dir := filepath.Join(root, examplesRelDir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read examples dir %q: %v", examplesRelDir, err)
	}

	var files []exampleFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read example %q: %v", entry.Name(), err)
		}
		var doc Document
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse example %q: %v", entry.Name(), err)
		}
		files = append(files, exampleFile{name: entry.Name(), doc: doc})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files
}

// exampleBoolFlag reads spec.<section>.enabled as a boolean, defaulting to
// false when the section or the flag is absent (the API-server default).
func exampleBoolFlag(t *testing.T, spec map[string]interface{}, section, fileName string) bool {
	t.Helper()
	raw, present := spec[section]
	if !present {
		return false
	}
	sectionMap := asMap(t, raw, fileName+": spec."+section)
	enabled, present := sectionMap["enabled"]
	if !present {
		return false
	}
	b, ok := enabled.(bool)
	if !ok {
		t.Fatalf("%s: spec.%s.enabled: expected bool, got %T (%v)", fileName, section, enabled, enabled)
	}
	return b
}

// TestExampleShape asserts every example resource has the correct identity and
// that its name/namespace/tenant/environment are mutually consistent
// (Requirements 10.3, 10.4).
func TestExampleShape(t *testing.T) {
	files := enumerateExamples(t)
	if len(files) == 0 {
		t.Fatalf("no *.yaml examples found in %q", examplesRelDir)
	}

	for _, f := range files {
		t.Run(f.name, func(t *testing.T) {
			if got := f.doc["apiVersion"]; got != "platform.hello-crossplane.io/v1alpha1" {
				t.Errorf("apiVersion: got %#v, want %q", got, "platform.hello-crossplane.io/v1alpha1")
			}
			if got := f.doc["kind"]; got != "TenantEnvironment" {
				t.Errorf("kind: got %#v, want %q", got, "TenantEnvironment")
			}

			meta := asMap(t, f.doc["metadata"], f.name+": metadata")
			spec := asMap(t, f.doc["spec"], f.name+": spec")

			name, ok := meta["name"].(string)
			if !ok {
				t.Fatalf("metadata.name: expected string, got %T (%v)", meta["name"], meta["name"])
			}
			namespace, ok := meta["namespace"].(string)
			if !ok {
				t.Fatalf("metadata.namespace: expected string, got %T (%v)", meta["namespace"], meta["namespace"])
			}

			// Req 10.3: metadata.name == metadata.namespace.
			if name != namespace {
				t.Errorf("metadata.name (%q) must equal metadata.namespace (%q)", name, namespace)
			}

			tenant, ok := spec["tenant"].(string)
			if !ok {
				t.Fatalf("spec.tenant: expected string, got %T (%v)", spec["tenant"], spec["tenant"])
			}
			environment, ok := spec["environment"].(string)
			if !ok {
				t.Fatalf("spec.environment: expected string, got %T (%v)", spec["environment"], spec["environment"])
			}

			// Req 10.3: spec.tenant matches the tenant pattern.
			if !exampleTenantPattern.MatchString(tenant) {
				t.Errorf("spec.tenant %q does not match %s", tenant, exampleTenantPattern.String())
			}
			// Req 10.3: spec.environment is one of dev/staging/prod.
			if _, ok := exampleEnvironments[environment]; !ok {
				t.Errorf("spec.environment %q is not one of dev/staging/prod", environment)
			}

			// Req 10.4: name equals <tenant>-<env>.
			want := tenant + "-" + environment
			if name != want {
				t.Errorf("metadata.name %q must equal <tenant>-<env> %q", name, want)
			}
		})
	}
}

// TestExampleVariants asserts exactly one disabled variant (both table and
// repository disabled) and exactly one enabled variant (both enabled) exist
// among the examples (Requirements 10.1, 10.2).
func TestExampleVariants(t *testing.T) {
	files := enumerateExamples(t)
	if len(files) == 0 {
		t.Fatalf("no *.yaml examples found in %q", examplesRelDir)
	}

	var disabled, enabled []string
	for _, f := range files {
		spec := asMap(t, f.doc["spec"], f.name+": spec")
		tableEnabled := exampleBoolFlag(t, spec, "table", f.name)
		repoEnabled := exampleBoolFlag(t, spec, "repository", f.name)

		switch {
		case !tableEnabled && !repoEnabled:
			disabled = append(disabled, f.name)
		case tableEnabled && repoEnabled:
			enabled = append(enabled, f.name)
		}
	}

	if len(disabled) != 1 {
		t.Errorf("expected exactly one disabled example (table+repository both false), got %d: %v", len(disabled), disabled)
	}
	if len(enabled) != 1 {
		t.Errorf("expected exactly one enabled example (table+repository both true), got %d: %v", len(enabled), enabled)
	}
}
