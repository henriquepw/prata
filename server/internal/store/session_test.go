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

func TestSessionByToken_ResolvesUserEmailAndAccount(t *testing.T) {
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

	want := store.Session{
		UserID:    ownerID,
		Email:     "ana@example.com",
		AccountID: accountID,
		Language:  "pt-BR", DateFormat: "DD/MM/YYYY", NumberFormat: "1.234,56", DailyLimitMode: "DYNAMIC",
		Currency:  "BRL",
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	if session != want {
		t.Errorf("session = %+v, want %+v", session, want)
	}
}

func TestSessionByToken_PicksEarliestJoinedAccountLikeSignin(t *testing.T) {
	db := openWithUser(t)
	const olderAccountID = "01JXQ8ZA000000000000000000"
	_, err := db.ExecContext(context.Background(), "INSERT INTO accounts (id, name, created_by, created_at, updated_at) VALUES (?, 'Bia', ?, '', '')",
		olderAccountID, ownerID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.ExecContext(context.Background(), "INSERT INTO account_users (id, account_id, user_id, role, created_at) VALUES (?, ?, ?, 'MEMBER', ?)",
		"01JXQ8ZB000000000000000000", olderAccountID, ownerID, "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	token, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	session, err := db.SessionByToken(context.Background(), token, now)
	if err != nil {
		t.Fatal(err)
	}

	if session.AccountID != olderAccountID {
		t.Errorf("session account = %q, want the earliest joined %q", session.AccountID, olderAccountID)
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

func TestSaveSettings_StoresNothingWhenTheMoedaIsRefused(t *testing.T) {
	db := openWithUser(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	token, err := db.CreateSession(context.Background(), ownerID, now)
	if err != nil {
		t.Fatal(err)
	}

	preferences := store.Preferences{Language: "en", DateFormat: "YYYY-MM-DD", NumberFormat: "1,234.56", DailyLimitMode: "FIXED"}
	sealed, err := store.SealPluggyCredentials("chave", ownerID, store.PluggyCredentials{ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.SaveSettings(context.Background(), ownerID, accountID, preferences, "XYZ", sealed); err == nil {
		t.Fatal("SaveSettings with Moeda XYZ succeeded, want an error")
	}

	session, err := db.SessionByToken(context.Background(), token, now)
	if err != nil {
		t.Fatal(err)
	}

	want := store.Preferences{Language: "pt-BR", DateFormat: "DD/MM/YYYY", NumberFormat: "1.234,56", DailyLimitMode: "DYNAMIC"}
	if session.Preferences != want || session.Currency != "BRL" {
		t.Errorf("settings = %+v %q, want %+v %q", session.Preferences, session.Currency, want, "BRL")
	}

	if _, err := db.PluggyCredentials(context.Background(), "chave", ownerID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("PluggyCredentials = %v, want ErrNotFound after the refused save", err)
	}
}
