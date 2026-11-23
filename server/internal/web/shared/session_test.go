package shared

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocation(t *testing.T) {
	tests := []struct {
		name, cookie, want string
	}{
		{name: "browser zone", cookie: "America/Sao_Paulo", want: "America/Sao_Paulo"},
		{name: "unknown zone falls back to UTC", cookie: "Mars/Olympus", want: "UTC"},
		{name: "missing cookie falls back to UTC", want: "UTC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "tz", Value: tt.cookie})
			}

			if got := Location(req).String(); got != tt.want {
				t.Errorf("location = %q, want %q", got, tt.want)
			}
		})
	}
}
