package main

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	_ "modernc.org/sqlite"
)

// TestMain pins the local zone to UTC, the zone the web uses when the browser sends no tz cookie,
// so tests that read time.Now() agree with the server about which day it is.
func TestMain(m *testing.M) {
	time.Local = time.UTC

	os.Exit(m.Run())
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "prata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewHandler_Healthz(t *testing.T) {
	db := openStore(t)
	handler := newHandler(db, config{})

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Errorf("healthy database status = %d, want %d", health.Code, http.StatusOK)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	unhealthy := httptest.NewRecorder()
	handler.ServeHTTP(unhealthy, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if unhealthy.Code != http.StatusServiceUnavailable {
		t.Errorf("closed database status = %d, want %d", unhealthy.Code, http.StatusServiceUnavailable)
	}
}

func TestRun_Healthcheck(t *testing.T) {
	tests := []struct {
		name    string
		closeDB bool
		want    int
	}{
		{name: "healthy", want: 0},
		{name: "unhealthy", closeDB: true, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openStore(t)
			server := httptest.NewServer(newHandler(db, config{}))
			t.Cleanup(server.Close)
			if tt.closeDB {
				db.Close()
			}

			addr := strings.TrimPrefix(server.URL, "http://127.0.0.1")
			if got := run([]string{"healthcheck"}, []string{"PRATA_ADDR=" + addr}); got != tt.want {
				t.Errorf("healthcheck exit code = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRun_NewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prata.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 12"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	logs := captureLogs(t)
	if got := run(nil, []string{"PRATA_DB_PATH=" + path}); got != 1 {
		t.Errorf("newer database exit code = %d, want 1", got)
	}
	if !strings.Contains(logs.String(), "database schema version 12 is newer than this binary (1)") {
		t.Errorf("logs = %q, want schema version error", logs.String())
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(logger) })

	return &logs
}
