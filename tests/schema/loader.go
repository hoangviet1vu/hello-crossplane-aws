// Package schema is a self-contained guard-test harness for the
// TenantEnvironment API artifacts. It parses apis/tenantenvironments/
// definition.yaml and schema.yaml into comparable maps so later tests can
// assert their structure and check the two files agree.
//
// This module deliberately imports no Crossplane types: it only parses YAML
// and compares plain Go maps, so it stays trivially testable and buildable
// without the provider or function toolchain.
package schema

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// Relative paths, from the repository root, of the artifacts under test.
const (
	definitionRelPath = "apis/tenantenvironments/definition.yaml"
	schemaRelPath     = "apis/tenantenvironments/schema.yaml"
)

// repoRootMarker is a file that only exists at the repository root. It anchors
// the upward search so the loader works regardless of the test's working
// directory.
const repoRootMarker = "crossplane-project.yaml"

// Document is a parsed YAML artifact as a plain, comparable map.
type Document map[string]interface{}

// RepoRoot walks up from the current working directory until it finds the
// repository root marker, returning the absolute path of the root directory.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, repoRootMarker)); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root marker %q not found from working directory", repoRootMarker)
		}
		dir = parent
	}
}

// loadDocument reads and parses the artifact at relPath (relative to the repo
// root) into a Document. A missing file is reported so callers can decide
// whether that is acceptable; the artifacts may not exist yet while sibling
// tasks author them.
func loadDocument(relPath string) (Document, error) {
	root, err := RepoRoot()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(root, relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("artifact %q does not exist yet: %w", relPath, err)
		}
		return nil, fmt.Errorf("read %q: %w", relPath, err)
	}

	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %q: %w", relPath, err)
	}
	return doc, nil
}

// LoadDefinition reads and parses apis/tenantenvironments/definition.yaml (the
// authoritative XRD) into a comparable map.
func LoadDefinition() (Document, error) {
	return loadDocument(definitionRelPath)
}

// LoadSchema reads and parses apis/tenantenvironments/schema.yaml (the
// SimpleSchema source, kept as documentation) into a comparable map.
func LoadSchema() (Document, error) {
	return loadDocument(schemaRelPath)
}
