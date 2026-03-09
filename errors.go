package jobs

import "errors"

var (
	// ErrNotFound is returned when a job entity does not exist.
	ErrNotFound = errors.New("jobs: not found")

	// ErrAlreadyFinalized is returned when attempting to finalize a job that
	// is already in a terminal state (completed, failed, cancelled).
	ErrAlreadyFinalized = errors.New("jobs: already finalized")
)
