// Showcase server for the jobs UI components backed by a real Postgres database.
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
	"os"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/dsx/showcase"
	"github.com/laenen-partners/identity"
	"github.com/laenen-partners/jobs"
	jobspg "github.com/laenen-partners/jobs/postgres"
	jobsui "github.com/laenen-partners/jobs/ui"
)

const defaultDSN = "postgres://showcase:showcase@localhost:5488/jobs_showcase?sslmode=disable"

func main() {
	if err := showcase.Run(showcase.Config{
		Port: 3333,
		Identities: []showcase.Identity{
			{Name: "Admin", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "admin-1", Roles: []string{"admin"}},
			{Name: "Member", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "user-1", Roles: []string{"member"}},
			{Name: "Viewer", TenantID: "showcase", WorkspaceID: "ws-1", PrincipalID: "viewer-1", Roles: []string{"viewer"}},
		},
		Setup: func(ctx context.Context, r chi.Router) error {
			client, err := setupPostgres(ctx)
			if err != nil {
				return err
			}

			h := jobsui.NewHandlers(client, func(id identity.Context) jobsui.AccessScope {
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
			})

			r.Get("/fragments/jobs", h.JobList())
			r.Get("/fragments/jobs/{id}", h.JobDetail())
			r.Post("/fragments/jobs/{id}/cancel", h.CancelJob())

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

func setupPostgres(ctx context.Context) (*jobs.Client, error) {
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

	client := jobs.NewClient(jobspg.NewStore(pool))

	if err := seedData(ctx, client); err != nil {
		return nil, fmt.Errorf("seed data: %w", err)
	}
	slog.Info("data seeded")

	return client, nil
}

func seedData(ctx context.Context, c *jobs.Client) error {
	if _, err := c.GetByExternalReference(ctx, "showcase-doc-processing-42"); err == nil {
		slog.Info("data already seeded, skipping")
		return nil
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

	_, err = c.RegisterJob(ctx, jobs.RegisterJobParams{
		ExternalReference: "showcase-report-gen-15",
		JobType:           "report_generation",
		Tags:              []string{"tenant:showcase", "owner:user-1"},
		Metadata:          mustJSON(map[string]any{"priority": "low"}),
	})
	if err != nil {
		return fmt.Errorf("create job4: %w", err)
	}

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

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
