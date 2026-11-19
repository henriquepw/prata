package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	_ "modernc.org/sqlite"
)

const (
	accountID = "01JXQ8Z4000000000000000000"
	ownerID   = "01JXQ8Z5000000000000000000"
)

func openWithUser(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "prata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	signup := store.Signup{Email: "ana@example.com", Password: "correct horse", AccountID: accountID, OwnerID: ownerID}
	if _, err := db.CreateAccount(context.Background(), signup, false); err != nil {
		t.Fatal(err)
	}

	return db
}

func TestAuthenticate(t *testing.T) {
	db := openWithUser(t)
	tests := []struct {
		name, email, password string
		wantErr               error
	}{
		{name: "right password", email: "ana@example.com", password: "correct horse"},
		{name: "wrong password", email: "ana@example.com", password: "wrong", wantErr: store.ErrInvalidCredentials},
		{name: "unknown email", email: "bia@example.com", password: "correct horse", wantErr: store.ErrInvalidCredentials},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, err := db.Authenticate(context.Background(), tt.email, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr == nil && userID != ownerID {
				t.Errorf("Authenticate user = %q, want %q", userID, ownerID)
			}
		})
	}
}

func TestSessionByToken_ResolvesUserAndAccount(t *testing.T) {
	db := openWithUser(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	token, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	session, err := db.SessionByToken(context.Background(), token, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	want := store.Session{UserID: ownerID, AccountID: accountID, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	if session != want {
		t.Errorf("session = %+v, want %+v", session, want)
	}
}

func TestSessionByToken_RenewsInLastDay(t *testing.T) {
	db := openWithUser(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	token, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{name: "more than 24h left keeps expiry", at: now.Add(29*24*time.Hour - time.Minute), want: now.Add(30 * 24 * time.Hour)},
		{name: "24h left renews to now + 30d", at: now.Add(29 * 24 * time.Hour), want: now.Add(59 * 24 * time.Hour)},
		{name: "renewed session outlives the original expiry", at: now.Add(31 * 24 * time.Hour), want: now.Add(59 * 24 * time.Hour)},
	}
	for _, tt := range tests {
		session, err := db.SessionByToken(context.Background(), token, tt.at)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		if !session.ExpiresAt.Equal(tt.want) {
			t.Errorf("%s: expires at %s, want %s", tt.name, session.ExpiresAt, tt.want)
		}
	}
}

func TestSessionByToken_RejectsAndDeletesExpired(t *testing.T) {
	db := openWithUser(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	token, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	expiry := now.Add(30 * 24 * time.Hour)
	if _, err := db.SessionByToken(context.Background(), token, expiry); !errors.Is(err, store.ErrInvalidSession) {
		t.Fatalf("expired lookup error = %v, want %v", err, store.ErrInvalidSession)
	}

	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM sessions"); err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Errorf("sessions after expired lookup = %d, want 0", count)
	}
}

func TestDeleteSession_OnlyThatSession(t *testing.T) {
	db := openWithUser(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	first, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	second, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteSession(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	if _, err := db.SessionByToken(context.Background(), first, now); !errors.Is(err, store.ErrInvalidSession) {
		t.Errorf("deleted session error = %v, want %v", err, store.ErrInvalidSession)
	}

	if _, err := db.SessionByToken(context.Background(), second, now); err != nil {
		t.Errorf("other session error = %v, want nil", err)
	}
}
