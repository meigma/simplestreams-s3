// Package failure defines transport-neutral application error kinds.
package failure

import (
	"errors"
	"fmt"
)

// Kind identifies a stable application failure class.
type Kind string

const (
	// KindInvalidInput identifies malformed or invalid caller input.
	KindInvalidInput Kind = "invalid_input"
	// KindUnsupportedImage identifies an image class excluded from V1.
	KindUnsupportedImage Kind = "unsupported_image"
	// KindNotFound identifies a missing external resource.
	KindNotFound Kind = "not_found"
	// KindAlreadyExists identifies a create-only write collision.
	KindAlreadyExists Kind = "already_exists"
	// KindIntegrity identifies bytes that do not match their asserted identity.
	KindIntegrity Kind = "integrity_failure"
	// KindContentConflict identifies immutable content that already differs.
	KindContentConflict Kind = "content_conflict"
	// KindCatalogConflict identifies an incompatible catalog state.
	KindCatalogConflict Kind = "catalog_conflict"
	// KindPrecondition identifies a failed conditional operation.
	KindPrecondition Kind = "precondition_failed"
	// KindUnavailable identifies a transient upstream failure.
	KindUnavailable Kind = "unavailable"
	// KindDeadline identifies an exhausted operation deadline.
	KindDeadline Kind = "deadline_exceeded"
	// KindUnauthorized identifies an upstream authorization failure.
	KindUnauthorized Kind = "unauthorized_upstream"
	// KindCanceled identifies caller cancellation.
	KindCanceled Kind = "canceled"
	// KindInternal identifies an unexpected local failure.
	KindInternal Kind = "internal_failure"
)

// Error carries a stable kind while preserving an underlying cause.
type Error struct {
	kind Kind
	op   string
	err  error
}

// New creates a classified failure with a human-readable message.
func New(kind Kind, operation string, message string) error {
	return &Error{kind: kind, op: operation, err: errors.New(message)}
}

// Wrap classifies err at an application boundary.
func Wrap(kind Kind, operation string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{kind: kind, op: operation, err: err}
}

// Error returns operation context without discarding the wrapped cause.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.op == "" {
		return e.err.Error()
	}
	return fmt.Sprintf("%s: %v", e.op, e.err)
}

// Unwrap exposes the underlying failure cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Is reports whether target has the same stable failure kind.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && e.kind == other.kind
}

// KindOf returns the stable kind carried by err.
func KindOf(err error) Kind {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.kind
	}
	return KindInternal
}

// IsKind reports whether err carries kind.
func IsKind(err error, kind Kind) bool {
	return KindOf(err) == kind
}
