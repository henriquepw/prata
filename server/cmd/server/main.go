package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	_ "modernc.org/sqlite"
)

const (
	defaultDBPath = "/data/prata.db"
	defaultAddr   = ":8080"
)

type config struct {
	dbPath          string
	addr            string
	isSignupAllowed bool
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	os.Exit(run(os.Args[1:], os.Environ()))
}

func run(args, env []string) int {
	cfg := configFromEnv(env)
	if len(args) == 1 && args[0] == "healthcheck" {
		if err := healthcheck(cfg.addr); err != nil {
			slog.Error("healthcheck failed", "error", err)
			return 1
		}
		return 0
	}
	if len(args) > 0 {
		slog.Error("unknown command", "command", args[0])
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.dbPath)
	if err != nil {
		slog.Error("opening database", "error", err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("closing database", "error", err)
		}
	}()

	server := &http.Server{Addr: cfg.addr, Handler: newHandler(db, cfg)}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	slog.Info("serving", "addr", cfg.addr)

	select {
	case err := <-serveErr:
		slog.Error("serving", "error", err)
		return 1
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutting down server", "error", err)
		return 1
	}
	return 0
}

func configFromEnv(env []string) config {
	vars := map[string]string{}
	for _, pair := range env {
		key, value, _ := strings.Cut(pair, "=")
		vars[key] = value
	}
	return config{
		dbPath:          cmp.Or(vars["PRATA_DB_PATH"], defaultDBPath),
		addr:            cmp.Or(vars["PRATA_ADDR"], defaultAddr),
		isSignupAllowed: strings.EqualFold(vars["PRATA_ALLOW_SIGNUP"], "true"),
	}
}

func newHandler(db *store.Store, _ config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func healthcheck(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parsing PRATA_ADDR %q: %w", addr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("creating healthcheck request: %w", err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("requesting health endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
