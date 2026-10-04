// Package store is skua's Postgres pool and its schema. Migrations are SQL
// files compiled into the binary and applied at boot.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open connects and pings, so a bad DSN fails at boot rather than on the
// first query a module makes.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return pool, nil
}

//go:embed migrations/*.sql
var embedded embed.FS

// migrations is what Migrate applies; a test swaps in its own.
var migrations fs.FS = embedded

// lockKey is the advisory lock Migrate holds: "skua" in ASCII.
const lockKey = 0x736b7561

// Migrate applies every migration not yet recorded, in file name order, in
// one transaction: a failure leaves the schema as it was. The advisory lock
// makes two processes booting at once take turns instead of racing.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := apply(ctx, tx); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return tx.Commit(ctx)
}

func apply(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", lockKey); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `create table if not exists schema_migrations (
		version text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	files, _ := fs.Glob(migrations, "migrations/*.sql") // sorted; the pattern is valid
	for _, f := range files {
		sql, err := fs.ReadFile(migrations, f)
		if err != nil {
			return err
		}
		// Recording first makes "already applied" the insert's conflict: one
		// round trip per file. No arguments on the migration itself: pgx sends
		// it on the simple protocol, so a file may hold several statements.
		tag, err := tx.Exec(ctx, "insert into schema_migrations (version) values ($1) on conflict do nothing", f)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
	}
	return nil
}
