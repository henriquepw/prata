package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henriquepw/prata/server/internal/store"
)

func withStdin(t *testing.T, input string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })

	stdin := os.Stdin
	os.Stdin = file
	t.Cleanup(func() { os.Stdin = stdin })
}

func TestRun_ResetPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prata.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	handler := newHandler(db, config{})
	key := signupKey(t, handler, newSignup())
	cookie := login(t, handler)

	withStdin(t, "new secret\n")
	if got := run([]string{"reset-password", newSignup().Email}, []string{"PRATA_DB_PATH=" + path}); got != 0 {
		t.Fatalf("reset-password exit code = %d, want 0", got)
	}

	if got := postSignout(handler, "Bearer "+key); got != http.StatusUnauthorized {
		t.Errorf("old API key signout status = %d, want %d", got, http.StatusUnauthorized)
	}

	assertRedirectsToLogin(t, get(handler, "/", cookie))
	if got := postForm(handler, "/login", credentials()).Code; got != http.StatusUnprocessableEntity {
		t.Errorf("login with old password status = %d, want %d", got, http.StatusUnprocessableEntity)
	}

	fresh := url.Values{"email": {newSignup().Email}, "password": {"new secret"}}
	if got := postForm(handler, "/login", fresh).Code; got != http.StatusSeeOther {
		t.Errorf("login with new password status = %d, want %d", got, http.StatusSeeOther)
	}
}

func TestRun_ResetPasswordUnknownEmail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prata.db")
	logs := captureLogs(t)

	withStdin(t, "new secret\n")
	if got := run([]string{"reset-password", "nobody@example.com"}, []string{"PRATA_DB_PATH=" + path}); got != 1 {
		t.Errorf("unknown email exit code = %d, want 1", got)
	}

	if !strings.Contains(logs.String(), "no user with email nobody@example.com") {
		t.Errorf("logs = %q, want unknown email error", logs.String())
	}
}
