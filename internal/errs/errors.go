// Package errs contains shared error identities, never HTTP policy.
package errs

import "errors"

var DependencyUnavailable = errors.New("dependency unavailable")
