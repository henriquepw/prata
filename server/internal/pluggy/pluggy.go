// Package pluggy talks to the Pluggy API with the user's own client id and secret.
package pluggy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// DefaultURL is the base address of the real Pluggy API.
const DefaultURL = "https://api.pluggy.ai"

// ErrInvalidCredentials means Pluggy refused the client id and secret.
var ErrInvalidCredentials = errors.New("invalid pluggy credentials")

// ErrItemNotFound means Pluggy has no Item with that id for these credentials.
var ErrItemNotFound = errors.New("pluggy item not found")

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

// Item is a connection made at meu.pluggy.ai: its institution and the state of its last update.
type Item struct {
	Status          string `json:"status"`
	ExecutionStatus string `json:"executionStatus"`
	LastUpdatedAt   string `json:"lastUpdatedAt"`
	Connector       struct {
		Name string `json:"name"`
	} `json:"connector"`
}

// Institution is the name of the bank behind the Item.
func (i Item) Institution() string {
	return i.Connector.Name
}

// NeedsReconnect reports whether the Item waits for the user to act at meu.pluggy.ai.
func (i Item) NeedsReconnect() bool {
	return NeedsReconnect(i.Status, i.ExecutionStatus)
}

// NeedsReconnect reports whether an Item in this status and executionStatus waits for the user to reconnect it.
func NeedsReconnect(status, executionStatus string) bool {
	return slices.Contains([]string{"LOGIN_ERROR", "WAITING_USER_INPUT"}, status) || slices.Contains([]string{
		"INVALID_CREDENTIALS", "ACCOUNT_LOCKED", "ACCOUNT_NEEDS_ACTION", "ACCOUNT_CREDENTIALS_RESET",
		"USER_AUTHORIZATION_NOT_GRANTED", "USER_AUTHORIZATION_PENDING", "USER_INPUT_TIMEOUT", "INVALID_CREDENTIALS_MFA",
		"USER_AUTHORIZATION_REVOKED",
	}, executionStatus)
}

// Item reads the Item with that id, or ErrItemNotFound when it does not exist or the credentials cannot reach it.
func (c Client) Item(ctx context.Context, apiKey, id string) (Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/items/"+url.PathEscape(id), nil)
	if err != nil {
		return Item{}, fmt.Errorf("creating pluggy item request: %w", err)
	}

	req.Header.Set("X-API-KEY", apiKey)

	response, err := httpClient.Do(req)
	if err != nil {
		return Item{}, fmt.Errorf("requesting pluggy item: %w", err)
	}
	defer response.Body.Close()

	if slices.Contains([]int{http.StatusNotFound, http.StatusBadRequest, http.StatusForbidden}, response.StatusCode) {
		return Item{}, ErrItemNotFound
	}

	if response.StatusCode != http.StatusOK {
		return Item{}, fmt.Errorf("pluggy item returned %s", response.Status)
	}

	var item Item
	if err := json.NewDecoder(response.Body).Decode(&item); err != nil {
		return Item{}, fmt.Errorf("decoding pluggy item: %w", err)
	}

	return item, nil
}

// Account is a Conta bancária of an Item: a checking account (type BANK) or a credit card (type CREDIT).
type Account struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Currency string `json:"currencyCode"`
	Name     string `json:"name"`
}

// Accounts lists the Contas bancárias of the Item.
func (c Client) Accounts(ctx context.Context, apiKey, itemID string) ([]Account, error) {
	var page struct {
		Results []Account `json:"results"`
	}
	if err := c.getJSON(ctx, apiKey, "/accounts?"+url.Values{"itemId": {itemID}}.Encode(), &page); err != nil {
		return nil, fmt.Errorf("reading pluggy accounts: %w", err)
	}

	return page.Results, nil
}

// Transaction is a line of an Account. Amount is the literal Pluggy sends, in reais, and Date its ISO timestamp.
type Transaction struct {
	ID            string      `json:"id"`
	Status        string      `json:"status"`
	Amount        json.Number `json:"amount"`
	Date          string      `json:"date"`
	Description   string      `json:"description"`
	OperationType string      `json:"operationType"`
}

// Transactions lists the lines of the Account dated on or after dateFrom (YYYY-MM-DD), following the cursor
// Pluggy gives in `next` until it runs out.
func (c Client) Transactions(ctx context.Context, apiKey, accountID, dateFrom string) ([]Transaction, error) {
	var transactions []Transaction

	next := "?" + url.Values{"accountId": {accountID}, "dateFrom": {dateFrom}}.Encode()
	for next != "" {
		var page struct {
			Next    string        `json:"next"`
			Results []Transaction `json:"results"`
		}
		if err := c.getJSON(ctx, apiKey, "/v2/transactions"+next, &page); err != nil {
			return nil, fmt.Errorf("reading pluggy transactions: %w", err)
		}

		transactions = append(transactions, page.Results...)
		next = page.Next
	}

	return transactions, nil
}

// getJSON reads the JSON at path, relative to the API, into out.
func (c Client) getJSON(ctx context.Context, apiKey, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+path, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("X-API-KEY", apiKey)

	response, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("requesting: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("returned %s", response.Status)
	}

	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding: %w", err)
	}

	return nil
}
