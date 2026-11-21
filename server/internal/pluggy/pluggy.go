// Package pluggy talks to the Pluggy API with the user's own client id and secret.
package pluggy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DefaultURL is the base address of the real Pluggy API.
const DefaultURL = "https://api.pluggy.ai"

// ErrInvalidCredentials means Pluggy refused the client id and secret.
var ErrInvalidCredentials = errors.New("invalid pluggy credentials")

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Client calls the Pluggy API at URL.
type Client struct {
	URL string
}

// Authenticate trades the client id and secret for the API key the other calls send as X-API-KEY.
func (c Client) Authenticate(ctx context.Context, clientID, clientSecret string) (string, error) {
	body, err := json.Marshal(map[string]string{"clientId": clientID, "clientSecret": clientSecret})
	if err != nil {
		return "", fmt.Errorf("encoding pluggy credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/auth", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating pluggy auth request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	response, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting pluggy auth: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return "", ErrInvalidCredentials
	}

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pluggy auth returned %s", response.Status)
	}

	var auth struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(response.Body).Decode(&auth); err != nil {
		return "", fmt.Errorf("decoding pluggy auth: %w", err)
	}

	return auth.APIKey, nil
}
