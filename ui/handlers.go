package ui

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/laenen-partners/dsx/ds"
	"github.com/laenen-partners/identity"
	"github.com/laenen-partners/jobs"
	"github.com/starfederation/datastar-go/datastar"
)

// JobListSignals holds the client-side filter and sort state.
type JobListSignals struct {
	Status  string `json:"status"`
	SortBy  string `json:"sortBy"`
	SortDir string `json:"sortDir"`
}

// Handlers provides HTTP handlers for job UI fragments.
type Handlers struct {
	client *jobs.Client
	policy AccessPolicy
}

// NewHandlers creates fragment handlers backed by the given jobs Client.
// The policy function controls access scoping per caller.
func NewHandlers(client *jobs.Client, policy AccessPolicy) *Handlers {
	return &Handlers{client: client, policy: policy}
}

// JobList returns an HTTP handler that renders the job list table as an SSE patch.
func (h *Handlers) JobList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var signals JobListSignals
		_ = ds.ReadSignals("jobs", r, &signals)
		sse := datastar.NewSSE(w, r)

		scope := h.scopeFromRequest(r)
		filter := jobs.ListFilter{
			Tags: append([]string{}, scope.ListTags...),
		}

		if signals.Status != "" {
			filter.Tags = append(filter.Tags, signals.Status)
		}
		if signals.SortBy != "" {
			filter.SortBy = jobs.SortField(signals.SortBy)
		}
		if signals.SortDir != "" {
			filter.SortDir = jobs.SortDirection(signals.SortDir)
		}

		result, err := h.client.ListJobs(r.Context(), filter)
		if err != nil {
			slog.ErrorContext(r.Context(), "jobs ui: list jobs", "error", err)
			ds.Send.Toast(sse, ds.ToastError, "Failed to load jobs")
			return
		}

		tagFilter := jobs.ListFilter{Tags: scope.ListTags}
		availTags, _ := h.client.ListTags(r.Context(), tagFilter)

		ds.Send.Patch(sse, JobListTable(result, scope.CanCancel, signals, availTags))
	}
}

// JobDetail returns an HTTP handler that renders a job detail panel inside a drawer.
// Expects a {id} path parameter.
func (h *Handlers) JobDetail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("id")
		if jobID == "" {
			http.Error(w, "missing job id", http.StatusBadRequest)
			return
		}

		job, err := h.client.GetJob(r.Context(), jobID)
		if err != nil {
			slog.ErrorContext(r.Context(), "jobs ui: get job", "job_id", jobID, "error", err)
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}

		steps, _ := h.client.GetSteps(r.Context(), jobID)
		scope := h.scopeFromRequest(r)

		sse := datastar.NewSSE(w, r)
		ds.Send.Drawer(sse, JobDetailContent(job, steps, scope.CanCancel), ds.WithDrawerMaxWidth("max-w-xl"))
	}
}

// CancelJob returns an HTTP handler that cancels a job, re-renders the drawer
// and re-patches the job list.
// Expects a {id} path parameter.
func (h *Handlers) CancelJob() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("id")
		if jobID == "" {
			http.Error(w, "missing job id", http.StatusBadRequest)
			return
		}

		scope := h.scopeFromRequest(r)
		if !scope.CanCancel {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		var signals JobListSignals
		_ = ds.ReadSignals("jobs", r, &signals)
		sse := datastar.NewSSE(w, r)

		if err := h.client.CancelJob(r.Context(), jobID); err != nil {
			slog.ErrorContext(r.Context(), "jobs ui: cancel job", "job_id", jobID, "error", err)
			ds.Send.Toast(sse, ds.ToastError, "Failed to cancel job")
			return
		}

		job, err := h.client.GetJob(r.Context(), jobID)
		if err != nil {
			slog.ErrorContext(r.Context(), "jobs ui: get job after cancel", "job_id", jobID, "error", err)
			return
		}
		steps, _ := h.client.GetSteps(r.Context(), jobID)

		// Re-render drawer with updated job.
		ds.Send.Drawer(sse, JobDetailContent(job, steps, scope.CanCancel), ds.WithDrawerMaxWidth("max-w-xl"))

		// Re-patch job list with current filters.
		filter := jobs.ListFilter{Tags: append([]string{}, scope.ListTags...)}
		if signals.Status != "" {
			filter.Tags = append(filter.Tags, signals.Status)
		}
		if signals.SortBy != "" {
			filter.SortBy = jobs.SortField(signals.SortBy)
		}
		if signals.SortDir != "" {
			filter.SortDir = jobs.SortDirection(signals.SortDir)
		}
		result, _ := h.client.ListJobs(r.Context(), filter)
		availTags, _ := h.client.ListTags(r.Context(), jobs.ListFilter{Tags: scope.ListTags})
		ds.Send.Patch(sse, JobListTable(result, scope.CanCancel, signals, availTags))

		ds.Send.Toast(sse, ds.ToastSuccess, "Job cancelled")
	}
}

func (h *Handlers) scopeFromRequest(r *http.Request) AccessScope {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		return AccessScope{}
	}
	return h.policy(ident)
}

// signalsJSON returns a JSON string for initializing Datastar signals.
func signalsJSON(s JobListSignals) string {
	sortBy := s.SortBy
	if sortBy == "" {
		sortBy = "created_at"
	}
	sortDir := s.SortDir
	if sortDir == "" {
		sortDir = "desc"
	}
	return fmt.Sprintf(`{"jobs":{"status":"%s","sortBy":"%s","sortDir":"%s"}}`, s.Status, sortBy, sortDir)
}

// sortExpr returns a Datastar expression that toggles sort direction for a column.
func sortExpr(column string) string {
	return fmt.Sprintf(
		`$jobs.sortDir = ($jobs.sortBy === '%s' && $jobs.sortDir === 'desc') ? 'asc' : 'desc'; $jobs.sortBy = '%s'`,
		column, column,
	)
}
