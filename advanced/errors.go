package advanced

import "errors"

// ErrModelRequired is returned by the package helpers when they are called
// with a nil generate.Model.
var ErrModelRequired = errors.New("advanced: generator required")
