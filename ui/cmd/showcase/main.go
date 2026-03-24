// Showcase server for the jobs UI components backed by a real Postgres database.
// The UI talks to an embedded ConnectRPC server, exercising the full RPC path.
//
// Prerequisites: docker compose up -d (from repo root)
// Run with: go run ./cmd/showcase
// Then open http://localhost:3333
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/dsx/showcase"
	"github.com/laenen-partners/dsx/stream"
	"github.com/laenen-partners/identity"
	"github.com/laenen-partners/jobs"
	jobsrpc "github.com/laenen-partners/jobs/connectrpc"
	"github.com/laenen-partners/jobs/connectrpc/gen/jobs/v1/jobsv1connect"
	jobspg "github.com/laenen-partners/jobs/postgres"
	jobsui "github.com/laenen-partners/jobs/ui"
	"github.com/laenen-partners/pubsub"
)

const defaultDSN = "postgres://showcase:showcase@localhost:5488/jobs_showcase?sslmode=disable"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err := showcase.Run(showcase.Config{
		Port: 3334,
		Identities: []showcase.Identity{
			{Name: "Admin", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "admin-1", Roles: []string{"admin"}},
			{Name: "Member", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "user-1", Roles: []string{"member"}},
			{Name: "Viewer", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "viewer-1", Roles: []string{"viewer"}},
		},
		Setup: func(ctx context.Context, r chi.Router, bus *pubsub.Bus, relay *stream.Relay) error {
			rpcClient, directClient, err := setupRPC(ctx, bus.PubSub())
			if err != nil {
				return err
			}

			h := jobsui.NewHandlers(rpcClient, func(id identity.Context) jobsui.AccessScope {
				if id.HasRole("admin") {
					return jobsui.AccessScope{
						ListTags:  []string{"tenant:" + id.TenantID()},
						CanCancel: true,
					}
				}
				if id.HasRole("member") {
					return jobsui.AccessScope{
						ListTags:  []string{"tenant:" + id.TenantID(), "owner:" + id.PrincipalID()},
						CanCancel: true,
					}
				}
				return jobsui.AccessScope{
					ListTags: []string{"tenant:" + id.TenantID()},
				}
			}, jobsui.WithOnCancel(func(ctx context.Context, jobID string) error {
				slog.InfoContext(ctx, "showcase: cancel handler called", "job_id", jobID)
				return nil
			}))

			h.RegisterRoutes(r)

			// Start background simulator for live updates.
			// Use a service identity so change notifications are scoped correctly.
			simCtx := identity.WithContext(ctx, mustIdentity("showcase", "ws-1", "simulator", identity.PrincipalService))
			go simulateJobProgress(simCtx, directClient)

			return nil
		},
		Pages: map[string]templ.Component{
			"/": showcasePage(),
		},
	}); err != nil {
		slog.Error("showcase failed", "error", err)
		os.Exit(1)
	}
}

// setupRPC creates a Postgres-backed jobs.Client, mounts the ConnectRPC
// handler on an in-process httptest.Server, and returns both the RPC client
// wrapper (for UI handlers) and the direct jobs.Client (for the simulator).
func setupRPC(ctx context.Context, ps pubsub.PubSub) (*jobsrpc.Client, *jobs.Client, error) {
	client, err := setupPostgres(ctx, ps)
	if err != nil {
		return nil, nil, err
	}

	// Mount ConnectRPC handlers on an in-process HTTP server.
	mux := http.NewServeMux()
	handler := jobsrpc.NewHandler(client)
	mux.Handle(jobsv1connect.NewJobQueryServiceHandler(handler))
	mux.Handle(jobsv1connect.NewJobCommandServiceHandler(handler))
	ts := httptest.NewServer(mux)

	slog.Info("embedded ConnectRPC server started", "url", ts.URL)

	return jobsrpc.NewClientFromHTTP(ts.Client(), ts.URL), client, nil
}

func setupPostgres(ctx context.Context, ps pubsub.PubSub) (*jobs.Client, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres (is docker compose up?): %w", err)
	}

	if err := jobspg.Migrate(ctx, pool, ""); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	client := jobs.NewClient(jobspg.NewStore(pool), jobs.WithPubSub(ps))

	// Seed and simulate with a service identity so change notifications
	// are published with the correct tenant/workspace scope.
	svcCtx := identity.WithContext(ctx, mustIdentity("showcase", "ws-1", "showcase-svc", identity.PrincipalService))

	if err := seedData(svcCtx, client); err != nil {
		return nil, fmt.Errorf("seed data: %w", err)
	}
	slog.Info("data seeded")

	return client, nil
}

func seedData(ctx context.Context, c *jobs.Client) error {
	if _, err := c.GetByExternalReference(ctx, "showcase-doc-processing-42"); err == nil {
		slog.Info("data already seeded, ensuring active jobs exist")
		return ensureActiveJobs(ctx, c)
	}

	job1, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-doc-processing-42",
		JobType:           "document_processing",
		Tags:              []string{"tenant:showcase", "owner:user-1"},
		Input:             mustJSON(map[string]any{"document_id": "doc-123", "pages": 10}),
	})
	if err != nil {
		return fmt.Errorf("create job1: %w", err)
	}
	_ = c.ReportProgress(ctx, job1.ID, jobs.Progress{Step: "extract", Current: 10, Total: 10, Message: "Done"})
	s1, _ := c.RegisterStep(ctx, job1.ID, jobs.RegisterStepParams{Name: "validate", Sequence: 1})
	_ = c.CompleteStep(ctx, s1.ID, jobs.CompleteStepParams{})
	s2, _ := c.RegisterStep(ctx, job1.ID, jobs.RegisterStepParams{Name: "extract", Sequence: 2})
	_ = c.CompleteStep(ctx, s2.ID, jobs.CompleteStepParams{Output: mustJSON(map[string]any{"entities": 5})})
	s3, _ := c.RegisterStep(ctx, job1.ID, jobs.RegisterStepParams{Name: "transform", Sequence: 3})
	_ = c.CompleteStep(ctx, s3.ID, jobs.CompleteStepParams{})
	_ = c.FinalizeJob(ctx, job1.ID, jobs.FinalizeParams{
		Status: jobs.StatusCompleted,
		Output: mustJSON(map[string]any{"result_url": "/results/batch-42"}),
		Tags:   []string{"output:result-42"},
	})

	job2, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-file-upload-99",
		JobType:           "file_processing",
		Tags:              []string{"tenant:showcase", "owner:user-1", "input:file-99"},
		Input:             mustJSON(map[string]any{"file": "report.pdf", "size_mb": 12}),
	})
	if err != nil {
		return fmt.Errorf("create job2: %w", err)
	}
	_ = c.ReportProgress(ctx, job2.ID, jobs.Progress{Step: "ocr", Current: 3, Total: 8, Message: "Processing page 3 of 8"})
	rs1, _ := c.RegisterStep(ctx, job2.ID, jobs.RegisterStepParams{Name: "upload", Sequence: 1})
	_ = c.CompleteStep(ctx, rs1.ID, jobs.CompleteStepParams{})
	_, _ = c.RegisterStep(ctx, job2.ID, jobs.RegisterStepParams{Name: "ocr", Sequence: 2})

	job3, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-entity-resolve-7",
		JobType:           "entity_resolution",
		Tags:              []string{"tenant:showcase", "owner:admin-1"},
	})
	if err != nil {
		return fmt.Errorf("create job3: %w", err)
	}
	_ = c.ReportProgress(ctx, job3.ID, jobs.Progress{Step: "resolve", Current: 1, Total: 5})
	fs1, _ := c.RegisterStep(ctx, job3.ID, jobs.RegisterStepParams{Name: "fetch", Sequence: 1})
	_ = c.CompleteStep(ctx, fs1.ID, jobs.CompleteStepParams{})
	fs2, _ := c.RegisterStep(ctx, job3.ID, jobs.RegisterStepParams{Name: "resolve", Sequence: 2})
	_ = c.FailStep(ctx, fs2.ID, "connection timeout: upstream service unavailable after 3 retries")
	_ = c.FinalizeJob(ctx, job3.ID, jobs.FinalizeParams{
		Status: jobs.StatusFailed,
		Error:  "connection timeout: upstream service unavailable after 3 retries",
	})

	job4, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-report-gen-15",
		JobType:           "report_generation",
		Tags:              []string{"tenant:showcase", "owner:user-1"},
		Metadata:          mustJSON(map[string]any{"priority": "low"}),
	})
	if err != nil {
		return fmt.Errorf("create job4: %w", err)
	}
	_ = c.ReportProgress(ctx, job4.ID, jobs.Progress{Message: "Generating report..."})

	job5, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-data-export-3",
		JobType:           "data_export",
		Tags:              []string{"tenant:showcase", "owner:admin-1"},
	})
	if err != nil {
		return fmt.Errorf("create job5: %w", err)
	}
	_ = c.ReportProgress(ctx, job5.ID, jobs.Progress{Step: "query", Current: 100, Total: 500})
	_ = c.CancelJob(ctx, job5.ID)

	job6, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-import-csv-88",
		JobType:           "csv_import",
		Tags:              []string{"tenant:showcase", "owner:user-1", "input:csv-88"},
		Input:             mustJSON(map[string]any{"file": "transactions.csv", "rows": 1200}),
	})
	if err != nil {
		return fmt.Errorf("create job6: %w", err)
	}
	_ = c.ReportProgress(ctx, job6.ID, jobs.Progress{Current: 450, Total: 1200, Message: "Importing rows..."})

	return nil
}

// ensureActiveJobs creates fresh running jobs when all previous ones have been
// cancelled or completed, so the showcase always has something to cancel.
func ensureActiveJobs(ctx context.Context, c *jobs.Client) error {
	active, err := c.ListJobs(ctx, jobs.ListFilter{Tags: []string{"tenant:showcase", "running"}})
	if err != nil {
		return fmt.Errorf("list active jobs: %w", err)
	}
	if len(active) > 0 {
		slog.Info("active jobs exist, nothing to create", "count", len(active))
		return nil
	}

	slog.Info("no active jobs found, creating fresh ones")

	job, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: fmt.Sprintf("showcase-live-sync-%d", time.Now().Unix()),
		JobType:           "data_sync",
		Tags:              []string{"tenant:showcase", "owner:user-1"},
		Input:             mustJSON(map[string]any{"source": "api", "target": "warehouse"}),
	})
	if err != nil {
		return fmt.Errorf("create active job: %w", err)
	}
	_ = c.ReportProgress(ctx, job.ID, jobs.Progress{Step: "sync", Current: 42, Total: 200, Message: "Syncing records..."})
	s1, _ := c.RegisterStep(ctx, job.ID, jobs.RegisterStepParams{Name: "fetch", Sequence: 1})
	_ = c.CompleteStep(ctx, s1.ID, jobs.CompleteStepParams{})
	_, _ = c.RegisterStep(ctx, job.ID, jobs.RegisterStepParams{Name: "sync", Sequence: 2})

	job2, err := c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: fmt.Sprintf("showcase-bg-process-%d", time.Now().Unix()),
		JobType:           "background_task",
		Tags:              []string{"tenant:showcase", "owner:admin-1"},
	})
	if err != nil {
		return fmt.Errorf("create active job 2: %w", err)
	}
	_ = c.ReportProgress(ctx, job2.ID, jobs.Progress{Message: "Working..."})

	return nil
}

// simulateJobProgress periodically advances running jobs to demonstrate
// live streaming updates in the showcase.
func simulateJobProgress(ctx context.Context, c *jobs.Client) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			running, err := c.ListJobs(ctx, jobs.ListFilter{Tags: []string{"tenant:showcase", "running"}})
			if err != nil {
				slog.DebugContext(ctx, "showcase: simulator list failed", "error", err)
				continue
			}
			slog.DebugContext(ctx, "showcase: simulator tick", "running_jobs", len(running))
			for _, job := range running {
				if job.Progress == nil {
					_ = c.ReportProgress(ctx, job.ID, jobs.Progress{Current: 1, Total: 100, Message: "Starting..."})
					continue
				}
				if job.Progress.Total == 0 {
					_ = c.ReportProgress(ctx, job.ID, jobs.Progress{
						Step:    job.Progress.Step,
						Current: 1,
						Total:   100,
						Message: job.Progress.Message,
					})
					continue
				}
				if job.Progress.Current < job.Progress.Total {
					next := job.Progress.Current + 1
					p := jobs.Progress{
						Step:    job.Progress.Step,
						Current: next,
						Total:   job.Progress.Total,
						Message: fmt.Sprintf("Processing %d/%d...", next, job.Progress.Total),
					}
					_ = c.ReportProgress(ctx, job.ID, p)

					if next >= job.Progress.Total {
						// Complete any running steps and finalize.
						steps, _ := c.GetSteps(ctx, job.ID)
						for _, s := range steps {
							if s.Status == jobs.StatusRunning {
								_ = c.CompleteStep(ctx, s.ID, jobs.CompleteStepParams{})
							}
						}
						_ = c.FinalizeJob(ctx, job.ID, jobs.FinalizeParams{
							Status: jobs.StatusCompleted,
						})
						slog.InfoContext(ctx, "showcase: job completed by simulator", "job_id", job.ID)
					}
				}
			}
		}
	}
}

func mustIdentity(tenantID, workspaceID, principalID string, pt identity.PrincipalType) identity.Context {
	id, err := identity.New(tenantID, workspaceID, principalID, pt, nil)
	if err != nil {
		panic(err)
	}
	return id
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
