package jobs

import "errors"

var (
	// ErrNotFound is returned when a job entity does not exist.
	ErrNotFound = errors.New("jobs: not found")

	// ErrAlreadyFinalized is returned when attempting to finalize a job that
	// is already in a terminal state (completed, failed, cancelled).
	ErrAlreadyFinalized = errors.New("jobs: already finalized")

	// ErrNoStore is returned when a direct operation is called on a Client
	// with no backing store (e.g. NoopClient).
	ErrNoStore = errors.New("jobs: no store configured")

	// ErrAccessDenied is returned when an entity exists but is not visible
	// in the current scope.
	ErrAccessDenied = errors.New("jobs: access denied")
)
