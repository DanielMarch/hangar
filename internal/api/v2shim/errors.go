package v2shim

import (
	"errors"
	"fmt"
	"net/http"
)

// shimError is an error that carries the HTTP status and the message
// legacy would have put on the wire.
type shimError struct {
	Status  int
	Message string
	Err     error
}

func (e *shimError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *shimError) Unwrap() error { return e.Err }

func internalError(what string, err error) error {
	return &shimError{Status: http.StatusInternalServerError, Message: "Server Error", Err: fmt.Errorf("%s: %w", what, err)}
}

var errBadID = &shimError{
	Status:  http.StatusBadRequest,
	Message: "The given data was invalid.",
}

// errShimNotFound is a single-resource route whose id does not exist.
// Laravel's implicit route-model binding answered a bare-string 404 body
// here, the same shape §6.5 records for every other error on this surface —
// so this is a 404 with legacy's body, not Huma's problem+json.
var errShimNotFound = &shimError{
	Status:  http.StatusNotFound,
	Message: "Not Found",
}

// errFilterUnsupported answers legacy's OData `$filter` query parameter.
//
// ── WHY THE SHIM REFUSES RATHER THAN IGNORES ─────────────────────────────
// Ignoring an unrecognised filter is the tempting option and it is the
// dangerous one. `$filter` NARROWS a result set; a client that asks for
// "contacts where standing < 0" and silently receives every contact has
// been handed data it did not ask for, and — where that client is showing
// the result to someone — data it may not have intended to expose. A loud
// 400 costs the integrator a code change they were going to have to make
// anyway; a silent widening costs them a bug they will not notice.
//
// So: not implemented, and said out loud, with a pointer to /api/v1's
// filter specification.
var errFilterUnsupported = &shimError{
	Status: http.StatusBadRequest,
	Message: "The $filter (OData) query parameter is not supported by the /api/v2 shim. " +
		"It is not silently ignored, because a filter that narrows a result set and is " +
		"then dropped returns MORE data than was asked for. Use the /api/v1 filter " +
		"specification instead; see " + DeprecationDocsURL + ".",
}

// writeShimError renders an error in legacy's shape (a bare JSON string),
// with the deprecation headers still applied.
func writeShimError(w http.ResponseWriter, err error) {
	var shim *shimError
	if errors.As(err, &shim) {
		writeLegacyError(w, shim.Status, shim.Message)
		return
	}
	writeLegacyError(w, http.StatusInternalServerError, "Server Error")
}
