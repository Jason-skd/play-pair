// Package errs contains shared error identities, never HTTP policy.
package errs

import "errors"

// DependencyUnavailable identifies a failed external dependency operation.
var DependencyUnavailable = errors.New("dependency unavailable")

// VersionConflict identifies a stale resource replacement.
var VersionConflict = errors.New("version conflict")

// NotFound identifies an absent business resource.
var NotFound = errors.New("not found")
