// Package api serves the device sync API under /api.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

type actorKey struct{}

type handler struct {
	db              *store.Store
	isSignupAllowed bool
}

// New returns the /api routes. Routes wrapped in requireAPIKey see the caller's actor via actorFrom.
func New(db *store.Store, isSignupAllowed bool) http.Handler {
	h := handler{db: db, isSignupAllowed: isSignupAllowed}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/signup", h.signup)
	mux.Handle("POST /api/auth/signout", h.requireAPIKey(h.signout))

	return mux
}

// requireAPIKey derives the actor only from the Bearer API key and rejects missing, unknown or revoked keys.
func (h handler) requireAPIKey(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || key == "" {
			writeReason(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}

		actor, err := h.db.ActorByAPIKey(r.Context(), key)
		if errors.Is(err, store.ErrInvalidAPIKey) {
			writeReason(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}

		if err != nil {
			internalError(w, "authenticating api key", err)
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
	})
}

func actorFrom(ctx context.Context) store.Actor {
	return ctx.Value(actorKey{}).(store.Actor)
}

func (h handler) signup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceName string `json:"device_name"`
		AccountID  string `json:"account_id"`
		OwnerID    string `json:"owner_id"`
		DeviceID   string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !isValidSignup(body.Email, body.Password, body.AccountID, body.OwnerID, body.DeviceID) {
		writeReason(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}

	key, err := h.db.CreateAccount(r.Context(), store.Signup(body), h.isSignupAllowed)
	switch {
	case errors.Is(err, store.ErrSignupClosed):
		writeReason(w, http.StatusForbidden, "SIGNUP_CLOSED")
	case errors.Is(err, store.ErrDuplicateID):
		writeReason(w, http.StatusConflict, "DUPLICATE_ID")
	case err != nil:
		internalError(w, "creating account", err)
	default:
		writeJSON(w, http.StatusCreated, map[string]string{"api_key": key})
	}
}

func (h handler) signout(w http.ResponseWriter, r *http.Request) {
	if err := h.db.RevokeAPIKey(r.Context(), actorFrom(r.Context()).DeviceID); err != nil {
		internalError(w, "revoking api key", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func isValidSignup(email, password string, ids ...string) bool {
	if !strings.Contains(email, "@") || password == "" {
		return false
	}

	for _, id := range ids {
		if _, err := ulid.ParseStrict(id); err != nil {
			return false
		}
	}

	return true
}

func writeReason(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, map[string]string{"reason": reason})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func internalError(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
