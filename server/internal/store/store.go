// Package store owns the server's SQLite connection and schema lifecycle.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/henriquepw/prata/shared/schema"
	"github.com/jmoiron/sqlx"
)

// Store is an open server database.
type Store struct {
	*sqlx.DB
}

// Open opens a SQLite database, enables WAL, and brings it to the current schema. Transactions take the write
// lock when they begin, waiting up to 5s for another writer, so a read-then-write cannot race.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlx.Open("sqlite", path+"?_txlock=immediate&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, errors.Join(fmt.Errorf("enabling wal: %w", err), db.Close())
	}

	if err := migrate(ctx, db); err != nil {
		return nil, errors.Join(fmt.Errorf("migrating database: %w", err), db.Close())
	}

	return &Store{db}, nil
}

func migrate(ctx context.Context, db *sqlx.DB) error {
	var version int
	if err := db.GetContext(ctx, &version, "PRAGMA user_version"); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	migrations, err := fs.Glob(schema.Files, "migration/*.sql")
	if err != nil {
		return fmt.Errorf("listing migrations: %w", err)
	}

	latest := len(migrations)
	if version > latest {
		return fmt.Errorf("database schema version %d is newer than this binary (%d)", version, latest)
	}

	if version < latest {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("starting migration: %w", err)
		}
		defer tx.Rollback()

		for i, name := range migrations[version:] {
			if err := applyMigration(ctx, tx, version+i+1, name); err != nil {
				return err
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration: %w", err)
		}
	}

	views, err := schema.Files.ReadFile("views.sql")
	if err != nil {
		return fmt.Errorf("reading views schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(views)); err != nil {
		return fmt.Errorf("recreating views: %w", err)
	}

	return nil
}

func applyMigration(ctx context.Context, tx *sqlx.Tx, version int, name string) error {
	sql, err := schema.Files.ReadFile(name)
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}

	if _, err := tx.ExecContext(ctx, string(sql)); err != nil {
		return fmt.Errorf("applying %s: %w", name, err)
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("recording schema version %d: %w", version, err)
	}

	return nil
}
