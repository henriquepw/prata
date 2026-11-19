// Package api serves the device sync API under /api.
package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
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
	mux.HandleFunc("POST /api/auth/signin", h.signin)
	mux.Handle("POST /api/auth/signout", h.requireAPIKey(h.signout))
	mux.Handle("POST /api/sync/push", h.requireAPIKey(h.push))
	mux.Handle("GET /api/sync/pull", h.requireAPIKey(h.pull))
	mux.Handle("GET /api/sync/snapshot", h.requireAPIKey(h.snapshot))

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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !isValidAuth(body.Email, body.Password, body.AccountID, body.OwnerID, body.DeviceID) {
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

func (h handler) signin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !isValidAuth(body.Email, body.Password, body.DeviceID) {
		writeReason(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}

	actor, key, err := h.db.Signin(r.Context(), body.Email, body.Password, body.DeviceID, body.DeviceName)
	switch {
	case errors.Is(err, store.ErrInvalidCredentials):
		writeReason(w, http.StatusUnauthorized, "INVALID_CREDENTIALS")
	case errors.Is(err, store.ErrDuplicateID):
		writeReason(w, http.StatusConflict, "DUPLICATE_ID")
	case err != nil:
		internalError(w, "signing in", err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"account_id": actor.AccountID, "owner_id": actor.UserID, "api_key": key})
	}
}

func (h handler) signout(w http.ResponseWriter, r *http.Request) {
	if err := h.db.RevokeAPIKey(r.Context(), actorFrom(r.Context()).DeviceID); err != nil {
		internalError(w, "revoking api key", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type ack struct {
	ID        string `json:"id"`
	ServerSeq int64  `json:"server_seq"`
}

type rejection struct {
	ID     string       `json:"id"`
	Reason store.Reason `json:"reason"`
}

// push commits the device's pending ops; device_id must match the API key's device. An op that does not
// decode is rejected on its own with INVALID_OPERATION.
func (h handler) push(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	var body struct {
		DeviceID string            `json:"device_id"`
		Ops      []json.RawMessage `json:"ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceID != actor.DeviceID {
		writeReason(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}

	ops := make([]store.Op, len(body.Ops))
	results := make([]store.Result, len(body.Ops))
	decoded := []store.Op{}
	for i, raw := range body.Ops {
		if err := json.Unmarshal(raw, &ops[i]); err != nil {
			results[i].Reason = store.ReasonInvalidOperation
			continue
		}

		decoded = append(decoded, ops[i])
	}

	committed, err := h.db.CommitOps(r.Context(), actor, decoded)
	if err != nil {
		internalError(w, "committing ops", err)
		return
	}

	for i := range results {
		if results[i].Reason == "" {
			results[i], committed = committed[0], committed[1:]
		}
	}

	response := struct {
		Acks     []ack       `json:"acks"`
		Rejected []rejection `json:"rejected"`
	}{Acks: []ack{}, Rejected: []rejection{}}
	for i, result := range results {
		if result.Reason != "" {
			response.Rejected = append(response.Rejected, rejection{ops[i].ID, result.Reason})
		} else {
			response.Acks = append(response.Acks, ack{ops[i].ID, result.Seq})
		}
	}

	writeJSON(w, http.StatusOK, response)
}

// pull returns the account's ops after cursor; device_id must match the API key's device.
func (h handler) pull(w http.ResponseWriter, r *http.Request) {
	const maxLimit = 500
	actor := actorFrom(r.Context())
	query := r.URL.Query()
	cursor, cursorErr := strconv.ParseInt(cmp.Or(query.Get("cursor"), "0"), 10, 64)
	limit, limitErr := strconv.Atoi(cmp.Or(query.Get("limit"), strconv.Itoa(maxLimit)))
	if cursorErr != nil || limitErr != nil || cursor < 0 || limit < 1 || query.Get("device_id") != actor.DeviceID {
		writeReason(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}

	page, err := h.db.Pull(r.Context(), actor, cursor, min(limit, maxLimit))
	if err != nil {
		internalError(w, "pulling ops", err)
		return
	}

	writeJSON(w, http.StatusOK, page)
}

// snapshot returns the account's domain state; device_id must match the API key's device.
func (h handler) snapshot(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r.Context())
	if r.URL.Query().Get("device_id") != actor.DeviceID {
		writeReason(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}

	snapshot, err := h.db.Snapshot(r.Context(), actor)
	if err != nil {
		internalError(w, "reading snapshot", err)
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

func isValidAuth(email, password string, ids ...string) bool {
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
