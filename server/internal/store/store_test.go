package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
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
