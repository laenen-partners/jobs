package postgres

import (
	"context"
	"embed"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/migrate"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

// DefaultScope is the default migration scope name.
const DefaultScope = "jobs"

// Migrate runs database migrations using the given pool.
// Scope isolates these migrations from other services sharing the same database.
// Pass "" to use DefaultScope.
func Migrate(ctx context.Context, pool *pgxpool.Pool, scope string) error {
	if scope == "" {
		scope = DefaultScope
	}
	sub, err := fs.Sub(migrationsFS, "db/migrations")
	if err != nil {
		return err
	}
	return migrate.Up(ctx, pool, sub, scope)
}
