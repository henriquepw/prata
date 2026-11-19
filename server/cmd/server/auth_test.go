package main

import (
	"context"
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

type signin struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
}

type signedIn struct {
	AccountID string `json:"account_id"`
	OwnerID   string `json:"owner_id"`
	APIKey    string `json:"api_key"`
}

func postSignin(t *testing.T, handler http.Handler, body signin) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/auth/signin", strings.NewReader(string(payload))))

	return response
}

func signinAs(t *testing.T, handler http.Handler, body signin) signedIn {
	t.Helper()
	response := postSignin(t, handler, body)
	if response.Code != http.StatusOK {
		t.Fatalf("signin status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var result signedIn
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	return result
}

func newSignin() signin {
	return signin{Email: "ana@example.com", Password: "correct horse", DeviceID: "01JXQ8Z9000000000000000000", DeviceName: "phone"}
}

func TestSignin_JoinsAccountWithNewDeviceKey(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	laptopKey := signupKey(t, handler, newSignup())

	got := signinAs(t, handler, newSignin())
	if got.AccountID != newSignup().AccountID || got.OwnerID != newSignup().OwnerID || got.APIKey == "" || got.APIKey == laptopKey {
		t.Fatalf("signin = %+v, want ana's account, owner and a new key", got)
	}
	if code := postSignout(handler, "Bearer "+got.APIKey); code != http.StatusNoContent {
		t.Errorf("signout with signin key status = %d, want %d", code, http.StatusNoContent)
	}
}

func TestSignin_ReconnectingDeviceGetsFreshKey(t *testing.T) {
	handler := newHandler(openStore(t), config{})
	signupKey(t, handler, newSignup())
	first := signinAs(t, handler, newSignin())
	postSignout(handler, "Bearer "+first.APIKey)

	again := signinAs(t, handler, newSignin())
	if again.APIKey == first.APIKey || postSignout(handler, "Bearer "+again.APIKey) != http.StatusNoContent {
		t.Errorf("reconnect signin = %+v, want a fresh working key", again)
	}
}

func TestSignin_RefusesWithoutCreatingDevice(t *testing.T) {
	db := openStore(t)
	handler := newHandler(db, config{isSignupAllowed: true})
	signupKey(t, handler, newSignup())
	signupKey(t, handler, signup{
		Email: "bia@example.com", Password: "another secret",
		AccountID: "01JXQ9Z4000000000000000000", OwnerID: "01JXQ9Z5000000000000000000", DeviceID: "01JXQ9Z6000000000000000000",
	})
	tests := []struct {
		name   string
		change func(*signin)
		status int
	}{
		{name: "wrong password", status: http.StatusUnauthorized, change: func(s *signin) { s.Password = "wrong" }},
		{name: "unknown email", status: http.StatusUnauthorized, change: func(s *signin) { s.Email = "eve@example.com" }},
		{name: "device of another user", status: http.StatusConflict, change: func(s *signin) { s.DeviceID = "01JXQ9Z6000000000000000000" }},
		{name: "invalid device id", status: http.StatusBadRequest, change: func(s *signin) { s.DeviceID = "not-a-ulid" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := newSignin()
			tt.change(&body)

			if got := postSignin(t, handler, body).Code; got != tt.status {
				t.Errorf("signin status = %d, want %d", got, tt.status)
			}

			var devices int
			if err := db.GetContext(context.Background(), &devices, "SELECT COUNT(*) FROM devices"); err != nil {
				t.Fatal(err)
			}
			if devices != 4 {
				t.Errorf("devices = %d, want the 4 from signup only", devices)
			}
		})
	}
}
