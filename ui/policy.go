// Package ui provides reusable DSX fragments and templ components for job tracking.
//
// The consuming app mounts fragment handlers on its router and injects an
// [AccessPolicy] to control what each caller can see:
//
//	h := jobsui.NewHandlers(client, policyFn)
//	r.Get("/fragments/jobs", h.JobList())
//	r.Get("/fragments/jobs/{id}", h.JobDetail())
package ui

import "github.com/laenen-partners/identity"

// AccessPolicy maps a caller's identity to the permissions they have
// within the jobs UI. The consuming app defines the policy at construction time.
type AccessPolicy func(identity.Context) AccessScope

// AccessScope controls what a caller can see and do.
type AccessScope struct {
	// ListTags are injected into every ListJobs query (AND-combined).
	// Use this to scope queries by tenant, owner, team, etc.
	ListTags []string

	// CanCancel controls whether the caller can cancel running jobs.
	CanCancel bool
}
