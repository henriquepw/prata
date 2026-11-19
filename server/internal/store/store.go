// Package store owns the server's SQLite connection and schema lifecycle.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/henriquepw/prata/shared/schema"
	"github.com/jmoiron/sqlx"
)

var migrations = []migration{{
	version: 1,
	files:   []string{"domain.sql", "server.sql"},
}}

type migration struct {
	version int
	files   []string
}

// Store is an open server database.
type Store struct {
	db *sqlx.DB
}

// Open opens a SQLite database, enables WAL, and brings it to the current schema.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, closeAfterOpenError(db, "enabling wal", err)
	}
	if err := migrate(ctx, db); err != nil {
		return nil, closeAfterOpenError(db, "migrating database", err)
	}

	return &Store{db: db}, nil
}

func closeAfterOpenError(db *sqlx.DB, operation string, err error) error {
	if closeErr := db.Close(); closeErr != nil {
		return fmt.Errorf("%s: %w", operation, errors.Join(err, closeErr))
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// PingContext checks that the underlying database responds.
func (s *Store) PingContext(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}
	return nil
}

func migrate(ctx context.Context, db *sqlx.DB) error {
	var version int
	if err := db.GetContext(ctx, &version, "PRAGMA user_version"); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	latest := migrations[len(migrations)-1].version
	if version > latest {
		return fmt.Errorf("database schema version %d is newer than this binary (%d)", version, latest)
	}
	if version < latest {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("starting migration: %w", err)
		}
		for _, migration := range migrations {
			if migration.version <= version {
				continue
			}
			if err := applyMigration(ctx, tx, migration); err != nil {
				return rollbackAfterMigrationError(tx, err)
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version)); err != nil {
				return rollbackAfterMigrationError(tx, fmt.Errorf("recording schema version: %w", err))
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

func rollbackAfterMigrationError(tx *sqlx.Tx, err error) error {
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		return errors.Join(err, fmt.Errorf("rolling back migration: %w", rollbackErr))
	}
	return err
}

func applyMigration(ctx context.Context, tx *sqlx.Tx, migration migration) error {
	for _, name := range migration.files {
		sql, err := schema.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(sql)); err != nil {
			return fmt.Errorf("applying %s: %w", name, err)
		}
	}
	return nil
}
