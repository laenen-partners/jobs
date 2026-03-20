// Package connectrpc provides Connect-RPC handlers for the jobs SDK.
//
// The handlers are thin translation layers that delegate to [jobs.Client].
// Mount them on your HTTP mux alongside your own middleware:
//
//	h := jobsrpc.NewHandler(client)
//	mux.Handle(jobsv1connect.NewJobQueryServiceHandler(h))
//	mux.Handle(jobsv1connect.NewJobCommandServiceHandler(h))
package connectrpc

import (
	"context"
	"encoding/json"

	connect "connectrpc.com/connect"

	"github.com/laenen-partners/jobs"
	jobsv1 "github.com/laenen-partners/jobs/connectrpc/gen/jobs/v1"
	"github.com/laenen-partners/jobs/connectrpc/gen/jobs/v1/jobsv1connect"
)

// Compile-time interface checks.
var (
	_ jobsv1connect.JobQueryServiceHandler   = (*Handler)(nil)
	_ jobsv1connect.JobCommandServiceHandler = (*Handler)(nil)
)

// Handler implements both JobQueryServiceHandler and JobCommandServiceHandler
// by delegating to a [jobs.Client].
type Handler struct {
	client *jobs.Client
}

// NewHandler creates Connect-RPC handlers backed by the given jobs Client.
func NewHandler(client *jobs.Client) *Handler {
	return &Handler{client: client}
}

// --- Query service ---

func (h *Handler) GetJob(ctx context.Context, req *connect.Request[jobsv1.GetJobRequest]) (*connect.Response[jobsv1.GetJobResponse], error) {
	job, err := h.client.GetJob(ctx, req.Msg.JobId)
	if err != nil {
		return nil, toConnectError(err)
	}
	steps, err := h.client.GetSteps(ctx, job.ID)
	if err != nil {
		return nil, toConnectError(err)
	}
	pj := jobToProto(job)
	pj.Steps = stepsToProto(steps)
	return connect.NewResponse(&jobsv1.GetJobResponse{Job: pj}), nil
}

func (h *Handler) GetJobByExternalReference(ctx context.Context, req *connect.Request[jobsv1.GetJobByExternalReferenceRequest]) (*connect.Response[jobsv1.GetJobByExternalReferenceResponse], error) {
	job, err := h.client.GetByExternalReference(ctx, req.Msg.ExternalReference)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.GetJobByExternalReferenceResponse{Job: jobToProto(job)}), nil
}

func (h *Handler) ListJobs(ctx context.Context, req *connect.Request[jobsv1.ListJobsRequest]) (*connect.Response[jobsv1.ListJobsResponse], error) {
	result, err := h.client.ListJobs(ctx, jobs.ListFilter{
		Tags:    req.Msg.Tags,
		Limit:   int(req.Msg.Limit),
		Offset:  int(req.Msg.Offset),
		SortBy:  jobs.SortField(req.Msg.SortBy),
		SortDir: jobs.SortDirection(req.Msg.SortDir),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	out := make([]*jobsv1.Job, len(result))
	for i := range result {
		out[i] = jobToProto(&result[i])
	}
	return connect.NewResponse(&jobsv1.ListJobsResponse{Jobs: out}), nil
}

func (h *Handler) ListTags(ctx context.Context, req *connect.Request[jobsv1.ListTagsRequest]) (*connect.Response[jobsv1.ListTagsResponse], error) {
	result, err := h.client.ListTags(ctx, jobs.ListFilter{
		Tags: req.Msg.Tags,
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.ListTagsResponse{Tags: result}), nil
}

func (h *Handler) GetSteps(ctx context.Context, req *connect.Request[jobsv1.GetStepsRequest]) (*connect.Response[jobsv1.GetStepsResponse], error) {
	result, err := h.client.GetSteps(ctx, req.Msg.JobId)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.GetStepsResponse{Steps: stepsToProto(result)}), nil
}

// --- Command service ---

func (h *Handler) RegisterJob(ctx context.Context, req *connect.Request[jobsv1.RegisterJobRequest]) (*connect.Response[jobsv1.RegisterJobResponse], error) {
	job, err := h.client.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: req.Msg.ExternalReference,
		JobType:           req.Msg.JobType,
		Tags:              req.Msg.Tags,
		Input:             json.RawMessage(req.Msg.Input),
		Metadata:          json.RawMessage(req.Msg.Metadata),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.RegisterJobResponse{Job: jobToProto(job)}), nil
}

func (h *Handler) FinalizeJob(ctx context.Context, req *connect.Request[jobsv1.FinalizeJobRequest]) (*connect.Response[jobsv1.FinalizeJobResponse], error) {
	err := h.client.FinalizeJob(ctx, req.Msg.JobId, jobs.FinalizeParams{
		Status: statusFromProto(req.Msg.Status),
		Error:  req.Msg.Error,
		Output: json.RawMessage(req.Msg.Output),
		Tags:   req.Msg.Tags,
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.FinalizeJobResponse{}), nil
}

func (h *Handler) ReportProgress(ctx context.Context, req *connect.Request[jobsv1.ReportProgressRequest]) (*connect.Response[jobsv1.ReportProgressResponse], error) {
	err := h.client.ReportProgress(ctx, req.Msg.JobId, jobs.Progress{
		Step:    req.Msg.Step,
		Current: int(req.Msg.Current),
		Total:   int(req.Msg.Total),
		Message: req.Msg.Message,
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.ReportProgressResponse{}), nil
}

func (h *Handler) CancelJob(ctx context.Context, req *connect.Request[jobsv1.CancelJobRequest]) (*connect.Response[jobsv1.CancelJobResponse], error) {
	if err := h.client.CancelJob(ctx, req.Msg.JobId); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.CancelJobResponse{}), nil
}

func (h *Handler) AddTags(ctx context.Context, req *connect.Request[jobsv1.AddTagsRequest]) (*connect.Response[jobsv1.AddTagsResponse], error) {
	if err := h.client.AddTags(ctx, req.Msg.JobId, req.Msg.Tags); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.AddTagsResponse{}), nil
}

func (h *Handler) RegisterStep(ctx context.Context, req *connect.Request[jobsv1.RegisterStepRequest]) (*connect.Response[jobsv1.RegisterStepResponse], error) {
	step, err := h.client.RegisterStep(ctx, req.Msg.JobId, jobs.RegisterStepParams{
		Name:     req.Msg.Name,
		Sequence: int(req.Msg.Sequence),
		Input:    json.RawMessage(req.Msg.Input),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.RegisterStepResponse{Step: stepToProto(step)}), nil
}

func (h *Handler) CompleteStep(ctx context.Context, req *connect.Request[jobsv1.CompleteStepRequest]) (*connect.Response[jobsv1.CompleteStepResponse], error) {
	err := h.client.CompleteStep(ctx, req.Msg.StepId, jobs.CompleteStepParams{
		Output: json.RawMessage(req.Msg.Output),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.CompleteStepResponse{}), nil
}

func (h *Handler) FailStep(ctx context.Context, req *connect.Request[jobsv1.FailStepRequest]) (*connect.Response[jobsv1.FailStepResponse], error) {
	if err := h.client.FailStep(ctx, req.Msg.StepId, req.Msg.Error); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&jobsv1.FailStepResponse{}), nil
}
