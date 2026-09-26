// Package errs contains shared error identities, never HTTP policy.
package errs

import "errors"

// DependencyUnavailable identifies a failed external dependency operation.
var DependencyUnavailable = errors.New("dependency unavailable")
