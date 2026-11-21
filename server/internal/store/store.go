// Package store owns the server's SQLite connection and schema lifecycle.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/henriquepw/prata/shared/schema"
	"github.com/jmoiron/sqlx"
)

var migrations = []migration{
	{version: 1, files: []string{"domain.sql", "server.sql"}},
	{version: 2, files: []string{"server_preferences.sql"}},
	{version: 3, files: []string{"server_categories.sql"}},
	{version: 4, files: []string{"server_income.sql"}},
	{version: 5, files: []string{"server_daily_limit.sql"}},
	{version: 6, files: []string{"server_default_category.sql"}},
	{version: 7, files: []string{"server_pluggy_credentials.sql"}},
}

type migration struct {
	version int
	files   []string
}

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

	latest := migrations[len(migrations)-1].version
	if version > latest {
		return fmt.Errorf("database schema version %d is newer than this binary (%d)", version, latest)
	}

	if version < latest {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("starting migration: %w", err)
		}
		defer tx.Rollback()

		for _, migration := range migrations[version:] {
			if err := applyMigration(ctx, tx, migration); err != nil {
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

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version)); err != nil {
		return fmt.Errorf("recording schema version %d: %w", migration.version, err)
	}

	return nil
}
