package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// fakePluggy is a Pluggy API on httptest that accepts only ClientID and ClientSecret at POST /auth and answers the
// other routes, registered with handle, only with the API key it hands out.
type fakePluggy struct {
	*httptest.Server
	mux *http.ServeMux
	// requests counts every request the fake received.
	requests   atomic.Int64
	mu         sync.Mutex
	dates      []string
	item       map[string]any
	itemServed bool
}

const (
	pluggyClientID     = "client-id-da-ana"
	pluggyClientSecret = "secret-da-ana"
	pluggyAPIKey       = "api-key-da-ana"
)

func newFakePluggy(t *testing.T) *fakePluggy {
	t.Helper()
	fake := &fakePluggy{mux: http.NewServeMux()}
	fake.mux.HandleFunc("POST /auth", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientID     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ClientID != pluggyClientID || body.ClientSecret != pluggyClientSecret {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 401, "message": "Invalid credentials"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"apiKey": pluggyAPIKey})
	})

	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.requests.Add(1)
		fake.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(fake.Close)

	return fake
}

// handle answers pattern, as "GET /items/{id}", with the status and the JSON of respond, or 403 without the API key.
func (f *fakePluggy) handle(pattern string, respond func(r *http.Request) (int, any)) {
	f.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != pluggyAPIKey {
			writeJSON(w, http.StatusForbidden, map[string]any{"code": 403, "message": "Missing or invalid authorization token"})
			return
		}

		status, body := respond(r)
		writeJSON(w, status, body)
	})
}

// config is a test config with Open Finance on, pointed at this fake.
func (f *fakePluggy) config() config {
	return config{encryptionKey: "chave-da-instancia", pluggyURL: f.URL}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
