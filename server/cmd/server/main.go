package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/henriquepw/prata/server/internal/api"
	"github.com/henriquepw/prata/server/internal/openfinance"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web"
	"github.com/henriquepw/prata/server/pkg/pluggy"
	_ "modernc.org/sqlite"
)

const (
	defaultDBPath = "/data/prata.db"
	defaultAddr   = ":8080"
	fetchInterval = 6 * time.Hour // fetchInterval is how often the server fetches the movements of every Connection.
)

type config struct {
	dbPath          string
	addr            string
	isSignupAllowed bool
	encryptionKey   string // encryptionKey encrypts the users' Pluggy credentials; empty turns Open Finance off.
	pluggyURL       string
}

// openFinance is the instance's Open Finance setup, shared by the web routes and the fetch job.
func (c config) openFinance() openfinance.Config {
	return openfinance.Config{Key: c.encryptionKey, Pluggy: pluggy.Client{URL: c.pluggyURL}}
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

	if len(args) == 2 && args[0] == "reset-password" {
		if err := resetPassword(cfg.dbPath, args[1]); err != nil {
			slog.Error("resetting password", "error", err)
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

	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()

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

	if cfg.openFinance().Enabled() {
		fetcher := openfinance.Fetcher{DB: db, Config: cfg.openFinance()}
		jobDone := make(chan struct{})
		defer func() {
			cancelJob()
			<-jobDone
		}()
		go func() {
			defer close(jobDone)
			fetchEvery(jobCtx, fetcher, fetchInterval)
		}()
	}

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

// fetchEvery fetches the movements of every Connection each interval until ctx ends; a failure is logged and
// waits for the next round.
func fetchEvery(ctx context.Context, fetcher openfinance.Fetcher, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := fetcher.Fetch(ctx, "", time.Now()); err != nil {
				slog.Warn("fetching connections", "error", err)
			}
		}
	}
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
		encryptionKey:   vars["PRATA_ENCRYPTION_KEY"],
		pluggyURL:       cmp.Or(vars["PRATA_PLUGGY_URL"], pluggy.DefaultURL),
	}
}

func newHandler(db *store.Store, cfg config) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(db, cfg.isSignupAllowed))
	mux.Handle("/", web.New(db, cfg.isSignupAllowed, cfg.openFinance()))
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

// resetPassword reads the new password from the first line of stdin, typed or piped.
func resetPassword(dbPath, email string) error {
	fmt.Fprint(os.Stderr, "New password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading password: %w", err)
	}

	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return errors.New("empty password")
	}

	ctx := context.Background()
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	return db.ResetPassword(ctx, email, password)
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
