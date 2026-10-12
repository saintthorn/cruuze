// Package db owns the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	"cruuze/server/internal/db/dbgen"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open connects a pool and verifies the database is reachable.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, url string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// InTx runs fn inside a transaction, committing if it returns nil.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(q *dbgen.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if err := fn(dbgen.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation reports whether err is a unique-constraint violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsNoRows reports whether err means a :one query matched nothing.
func IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
