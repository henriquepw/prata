package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type signup struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
	AccountID  string `json:"account_id"`
	OwnerID    string `json:"owner_id"`
	DeviceID   string `json:"device_id"`
}

func newSignup() signup {
	return signup{
		Email:      "ana@example.com",
		Password:   "correct horse",
		DeviceName: "laptop",
		AccountID:  "01JXQ8Z4000000000000000000",
		OwnerID:    "01JXQ8Z5000000000000000000",
		DeviceID:   "01JXQ8Z6000000000000000000",
	}
}

func postSignup(t *testing.T, handler http.Handler, body signup) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/auth/signup", strings.NewReader(string(payload))))

	return response
}

func signupKey(t *testing.T, handler http.Handler, body signup) string {
	t.Helper()
	response := postSignup(t, handler, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body)
	}

	var created struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	return created.APIKey
}

func postSignout(handler http.Handler, authorization string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signout", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	return response.Code
}

func TestSignup_ThenSignoutRevokesKey(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	key := signupKey(t, handler, newSignup())

	if got := postSignout(handler, "Bearer "+key); got != http.StatusNoContent {
		t.Fatalf("signout status = %d, want %d", got, http.StatusNoContent)
	}
	if got := postSignout(handler, "Bearer "+key); got != http.StatusUnauthorized {
		t.Errorf("signout with revoked key status = %d, want %d", got, http.StatusUnauthorized)
	}
}

func TestSignup_OnlyFirstUserUnlessAllowed(t *testing.T) {
	second := signup{
		Email:     "bia@example.com",
		Password:  "another secret",
		AccountID: "01JXQ9Z4000000000000000000",
		OwnerID:   "01JXQ9Z5000000000000000000",
		DeviceID:  "01JXQ9Z6000000000000000000",
	}
	tests := []struct {
		name string
		cfg  config
		want int
	}{
		{name: "blocked after first user", want: http.StatusForbidden},
		{name: "allowed by PRATA_ALLOW_SIGNUP", cfg: configFromEnv([]string{"PRATA_ALLOW_SIGNUP=true"}), want: http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newHandler(openStore(t), tt.cfg)
			signupKey(t, handler, newSignup())

			if got := postSignup(t, handler, second).Code; got != tt.want {
				t.Errorf("second signup status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSignup_DuplicateIDWritesNothing(t *testing.T) {
	tests := []struct {
		name      string
		duplicate func(*signup)
	}{
		{name: "account", duplicate: func(s *signup) { s.AccountID = newSignup().AccountID }},
		{name: "user", duplicate: func(s *signup) { s.OwnerID = newSignup().OwnerID }},
		{name: "email", duplicate: func(s *signup) { s.Email = newSignup().Email }},
		{name: "device", duplicate: func(s *signup) { s.DeviceID = newSignup().DeviceID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newHandler(openStore(t), config{isSignupAllowed: true})
			signupKey(t, handler, newSignup())

			fresh := signup{
				Email:     "bia@example.com",
				Password:  "another secret",
				AccountID: "01JXQ9Z4000000000000000000",
				OwnerID:   "01JXQ9Z5000000000000000000",
				DeviceID:  "01JXQ9Z6000000000000000000",
			}
			conflicting := fresh
			tt.duplicate(&conflicting)

			if got := postSignup(t, handler, conflicting).Code; got != http.StatusConflict {
				t.Fatalf("duplicate signup status = %d, want %d", got, http.StatusConflict)
			}
			signupKey(t, handler, fresh)
		})
	}
}

func TestSignup_RejectsInvalidRequest(t *testing.T) {
	invalid := newSignup()
	invalid.DeviceID = "not-a-ulid"

	if got := postSignup(t, newHandler(openStore(t), config{}), invalid).Code; got != http.StatusBadRequest {
		t.Errorf("invalid signup status = %d, want %d", got, http.StatusBadRequest)
	}
}

func TestAPI_RejectsMissingOrUnknownKey(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	key := signupKey(t, handler, newSignup())

	for _, authorization := range []string{"", "Bearer ", "Bearer wrong", key} {
		if got := postSignout(handler, authorization); got != http.StatusUnauthorized {
			t.Errorf("signout with Authorization %q status = %d, want %d", authorization, got, http.StatusUnauthorized)
		}
	}
}
