package connectrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	connect "connectrpc.com/connect"

	"github.com/laenen-partners/jobs"
	jobsv1 "github.com/laenen-partners/jobs/connectrpc/gen/jobs/v1"
	"github.com/laenen-partners/jobs/connectrpc/gen/jobs/v1/jobsv1connect"
)

// Client wraps the generated ConnectRPC clients and exposes domain-level
// methods. It satisfies [github.com/laenen-partners/jobs/ui.JobService].
type Client struct {
	query   jobsv1connect.JobQueryServiceClient
	command jobsv1connect.JobCommandServiceClient
}

// NewClient creates a ConnectRPC client targeting baseURL.
// Pass connect.WithHTTPClient(httpClient) to customise the transport
// (e.g. for an in-process httptest.Server).
func NewClient(baseURL string, opts ...connect.ClientOption) *Client {
	return &Client{
		query:   jobsv1connect.NewJobQueryServiceClient(http.DefaultClient, baseURL, opts...),
		command: jobsv1connect.NewJobCommandServiceClient(http.DefaultClient, baseURL, opts...),
	}
}

// NewClientFromHTTP creates a ConnectRPC client using the given http.Client
// and base URL. Useful for wiring to an httptest.Server.
func NewClientFromHTTP(httpClient *http.Client, baseURL string, opts ...connect.ClientOption) *Client {
	return &Client{
		query:   jobsv1connect.NewJobQueryServiceClient(httpClient, baseURL, opts...),
		command: jobsv1connect.NewJobCommandServiceClient(httpClient, baseURL, opts...),
	}
}

func (c *Client) ListJobs(ctx context.Context, filter jobs.ListFilter) ([]jobs.Job, error) {
	resp, err := c.query.ListJobs(ctx, connect.NewRequest(&jobsv1.ListJobsRequest{
		Tags:    filter.Tags,
		Limit:   int32(filter.Limit),
		Offset:  int32(filter.Offset),
		SortBy:  string(filter.SortBy),
		SortDir: string(filter.SortDir),
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs rpc: list: %w", err)
	}
	result := make([]jobs.Job, len(resp.Msg.Jobs))
	for i, pj := range resp.Msg.Jobs {
		result[i] = jobFromProto(pj)
	}
	return result, nil
}

func (c *Client) ListTags(ctx context.Context, filter jobs.ListFilter) ([]string, error) {
	resp, err := c.query.ListTags(ctx, connect.NewRequest(&jobsv1.ListTagsRequest{
		Tags: filter.Tags,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs rpc: list tags: %w", err)
	}
	return resp.Msg.Tags, nil
}

func (c *Client) GetJob(ctx context.Context, jobID string) (*jobs.Job, error) {
	resp, err := c.query.GetJob(ctx, connect.NewRequest(&jobsv1.GetJobRequest{
		JobId: jobID,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs rpc: get: %w", err)
	}
	j := jobFromProto(resp.Msg.Job)
	return &j, nil
}

func (c *Client) GetSteps(ctx context.Context, jobID string) ([]jobs.Step, error) {
	resp, err := c.query.GetSteps(ctx, connect.NewRequest(&jobsv1.GetStepsRequest{
		JobId: jobID,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs rpc: get steps: %w", err)
	}
	result := make([]jobs.Step, len(resp.Msg.Steps))
	for i, ps := range resp.Msg.Steps {
		result[i] = stepFromProto(ps)
	}
	return result, nil
}

func (c *Client) CancelJob(ctx context.Context, jobID string) error {
	_, err := c.command.CancelJob(ctx, connect.NewRequest(&jobsv1.CancelJobRequest{
		JobId: jobID,
	}))
	if err != nil {
		return fmt.Errorf("jobs rpc: cancel: %w", err)
	}
	return nil
}

// --- Proto → Domain converters (for client-side) ---

func jobFromProto(pj *jobsv1.Job) jobs.Job {
	j := jobs.Job{
		ID:                pj.Id,
		ExternalReference: pj.ExternalReference,
		JobType:           pj.JobType,
		Status:            statusFromProto(pj.Status),
		Error:             pj.Error,
		Input:             json.RawMessage(pj.Input),
		Output:            json.RawMessage(pj.Output),
		Metadata:          json.RawMessage(pj.Metadata),
		Tags:              pj.Tags,
	}
	j.CreatedAt, _ = time.Parse(time.RFC3339, pj.CreatedAt)
	j.UpdatedAt, _ = time.Parse(time.RFC3339, pj.UpdatedAt)
	if pj.Progress != nil {
		p := jobs.Progress{
			Step:    pj.Progress.Step,
			Current: int(pj.Progress.Current),
			Total:   int(pj.Progress.Total),
			Message: pj.Progress.Message,
		}
		p.UpdatedAt, _ = time.Parse(time.RFC3339, pj.Progress.UpdatedAt)
		j.Progress = &p
	}
	return j
}

func stepFromProto(ps *jobsv1.Step) jobs.Step {
	s := jobs.Step{
		ID:       ps.Id,
		JobID:    ps.JobId,
		Name:     ps.Name,
		Sequence: int(ps.Sequence),
		Status:   statusFromProto(ps.Status),
		Error:    ps.Error,
	}
	s.StartedAt, _ = time.Parse(time.RFC3339, ps.StartedAt)
	s.CompletedAt, _ = time.Parse(time.RFC3339, ps.CompletedAt)
	return s
}
