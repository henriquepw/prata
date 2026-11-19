package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henriquepw/prata/server/internal/store"
	_ "modernc.org/sqlite"
)

func TestNewHandler_Healthz(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "prata.db"))
	if err != nil {
		t.Fatal(err)
	}

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

func TestRun(t *testing.T) {
	t.Run("healthcheck", func(t *testing.T) {
		transport := http.DefaultTransport
		http.DefaultTransport = roundTripper(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "http://127.0.0.1:8080/healthz" {
				t.Errorf("healthcheck URL = %q", request.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		})
		t.Cleanup(func() {
			http.DefaultTransport = transport
		})

		if got := run([]string{"healthcheck"}, []string{"PRATA_ADDR=:8080"}); got != 0 {
			t.Errorf("healthy healthcheck exit code = %d, want 0", got)
		}
	})

	t.Run("unreachable healthcheck", func(t *testing.T) {
		transport := http.DefaultTransport
		http.DefaultTransport = roundTripper(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unreachable")
		})
		t.Cleanup(func() {
			http.DefaultTransport = transport
		})

		if got := run([]string{"healthcheck"}, []string{"PRATA_ADDR=:8080"}); got != 1 {
			t.Errorf("unhealthy healthcheck exit code = %d, want 1", got)
		}
	})

	t.Run("newer database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "prata.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		if got := run(nil, []string{"PRATA_DB_PATH=" + path}); got != 1 {
			t.Errorf("newer database exit code = %d, want 1", got)
		}
	})
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
