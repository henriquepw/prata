package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/shared/schema"
	_ "modernc.org/sqlite"
)

func TestCreateAccount_WaitsForConcurrentSignup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "prata.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })

	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.ExecContext(ctx, "INSERT INTO users (id, email, secret, created_at, updated_at) VALUES ('01JXQ8ZC000000000000000000', 'bia@example.com', '', '', '')"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error)
	go func() {
		_, err := db.CreateAccount(ctx, store.Signup{Email: "ana@example.com", Password: "secret", AccountID: accountID, OwnerID: ownerID}, false)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}

	if err := <-done; !errors.Is(err, store.ErrSignupClosed) {
		t.Errorf("signup racing another = %v, want %v", err, store.ErrSignupClosed)
	}
}

func TestOpen_MigratesExistingDatabaseToDefaultPreferencesAndCurrency(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "prata.db")
	v1, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"domain.sql", "server.sql"} {
		file, err := schema.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := v1.Exec(string(file)); err != nil {
			t.Fatal(err)
		}
	}

	for _, statement := range []string{
		"PRAGMA user_version = 1",
		"INSERT INTO users (id, email, secret, created_at, updated_at) VALUES ('" + ownerID + "', 'ana@example.com', '', '', '')",
		"INSERT INTO accounts (id, name, created_by, created_at, updated_at) VALUES ('" + accountID + "', 'Ana', '" + ownerID + "', '', '')",
	} {
		if _, err := v1.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := v1.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	var got struct {
		Language     string `db:"language"`
		DateFormat   string `db:"date_format"`
		NumberFormat string `db:"number_format"`
		Currency     string `db:"currency"`
	}
	query := "SELECT u.language, u.date_format, u.number_format, a.currency FROM users u, accounts a"
	if err := db.GetContext(ctx, &got, query); err != nil {
		t.Fatal(err)
	}

	if got.Language != "pt-BR" || got.DateFormat != "DD/MM/YYYY" || got.NumberFormat != "1.234,56" || got.Currency != "BRL" {
		t.Errorf("migrated user and account = %+v, want pt-BR, DD/MM/YYYY, 1.234,56 and BRL", got)
	}

	for _, query := range []string{
		"UPDATE users SET language = 'fr'",
		"UPDATE users SET date_format = 'DD.MM.YYYY'",
		"UPDATE users SET number_format = '1 234,56'",
		"UPDATE accounts SET currency = 'JPY'",
	} {
		if _, err := db.ExecContext(ctx, query); err == nil {
			t.Errorf("%q succeeded, want the CHECK to refuse it", query)
		}
	}
}
