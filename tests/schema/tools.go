//go:build tools

// This file pins test-only dependencies that are not yet imported by
// non-test source. The property-based tests added by later tasks (5.1, 5.2)
// import pgregory.net/rapid; retaining it here keeps `go mod tidy` from
// pruning the require before those tests exist. The `tools` build tag means
// this file is never compiled into the package.
package schema

import (
	_ "pgregory.net/rapid"
)
