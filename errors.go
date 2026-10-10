package jikko

import (
	"errors"
	"fmt"
)

// PermissionError reports that an actor lacks the capability an operation
// needs on a page. Harnesses match it with errors.As, or IsPermissionError,
// to answer "forbidden" rather than "invalid".
type PermissionError struct {
	Actor string
	Need  Capability
	Path  string
	// Reason, when set, says what needed the capability.
	Reason string
}

func (e *PermissionError) Error() string {
	msg := fmt.Sprintf("%s lacks %s permission on %s", e.Actor, e.Need, e.Path)
	if e.Reason != "" {
		msg += ", " + e.Reason
	}
	return msg
}

// IsPermissionError reports whether err is, or wraps, a PermissionError.
func IsPermissionError(err error) bool {
	var pe *PermissionError
	return errors.As(err, &pe)
}
