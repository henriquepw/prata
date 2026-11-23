package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/openfinance"
	"github.com/henriquepw/prata/server/internal/pluggy"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// connectionsView is the Conexões screen: the user's Conexões, the accounts to feed, whether the user has Pluggy
// credentials, the typed form with its field errors, and the error of the last "buscar agora".
type connectionsView struct {
	Connections    []store.Connection
	Accounts       []store.AccountRef
	HasCredentials bool
	ItemID         string
	AccountID      string
	Errors         map[string]string
	FetchError     string
}

func (h handler) connections(w http.ResponseWriter, r *http.Request) {
	h.renderConnections(w, r, http.StatusOK, connectionsView{Errors: map[string]string{}})
}

func (h handler) renderConnections(w http.ResponseWriter, r *http.Request, status int, view connectionsView) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	session := shared.SessionFrom(r.Context())

	var err error
	if view.Connections, err = h.db.Connections(r.Context(), session.UserID); err != nil {
		shared.InternalError(w, "listing connections", err)
		return
	}

	if view.Accounts, err = h.db.UserAccounts(r.Context(), session.UserID); err != nil {
		shared.InternalError(w, "listing accounts", err)
		return
	}

	_, err = h.db.PluggyCredentials(r.Context(), h.openFinance.Key, session.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		shared.InternalError(w, "reading pluggy credentials", err)
		return
	}

	view.HasCredentials = err == nil

	s, ok := h.shell(w, r, "nav.connections")
	if !ok {
		return
	}

	shared.Render(w, r, status, connectionsPage(s, view))
}

// saveConnection registers the Pluggy Item on the account after Pluggy confirms the Item exists for the user's
// credentials, and reloads the page, or answers 422 with the page and stores nothing when the Item is unknown or
// unreachable or the account is not BRL.
func (h handler) saveConnection(w http.ResponseWriter, r *http.Request) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	session := shared.SessionFrom(r.Context())
	view := connectionsView{ItemID: strings.TrimSpace(r.PostFormValue("item_id")), AccountID: r.PostFormValue("account_id"), Errors: map[string]string{}}

	item, err := h.fetchItem(r, session.UserID, view.ItemID)
	switch {
	case errors.Is(err, pluggy.ErrItemNotFound):
		view.Errors["item_id"] = i18n.T(r.Context(), "connections.error.item")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, pluggy.ErrInvalidCredentials):
		view.Errors["item_id"] = i18n.T(r.Context(), "settings.error.pluggy_rejected")
	case err != nil:
		slog.Warn("reading pluggy item", "error", err)
		view.Errors["item_id"] = i18n.T(r.Context(), "settings.error.pluggy_offline")
	}

	if len(view.Errors) == 0 {
		err = h.db.AddConnection(r.Context(), session.UserID, view.AccountID, view.ItemID, item)
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, store.ErrCurrency):
			view.Errors["account_id"] = i18n.T(r.Context(), "connections.error.currency")
		case err != nil:
			shared.InternalError(w, "adding connection", err)
			return
		}
	}

	if len(view.Errors) > 0 {
		h.renderConnections(w, r, http.StatusUnprocessableEntity, view)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// fetchItem reads the Pluggy Item with the user's saved credentials, or store.ErrNotFound when there are none.
func (h handler) fetchItem(r *http.Request, userID, itemID string) (pluggy.Item, error) {
	credentials, err := h.db.PluggyCredentials(r.Context(), h.openFinance.Key, userID)
	if err != nil {
		return pluggy.Item{}, err
	}

	apiKey, err := h.openFinance.Pluggy.Authenticate(r.Context(), credentials.ClientID, credentials.ClientSecret)
	if err != nil {
		return pluggy.Item{}, err
	}

	return h.openFinance.Pluggy.Item(r.Context(), apiKey, itemID)
}

// deleteConnection removes the user's Conexão and its pending movements, without calling Pluggy.
func (h handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	err := h.db.DeleteConnection(r.Context(), shared.SessionFrom(r.Context()).UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}

	if err != nil {
		shared.InternalError(w, "deleting connection", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// fetchConnections fetches the movements of the user's Conexões now, then reloads the screen. When a Conexão fails,
// the others are still fetched, the error is logged and the screen answers 502 with a warning over the Conexões, whose
// state shows any Item that asks to reconnect.
func (h handler) fetchConnections(w http.ResponseWriter, r *http.Request) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	fetcher := openfinance.Fetcher{DB: h.db, Key: h.openFinance.Key, Pluggy: h.openFinance.Pluggy}
	if err := fetcher.Fetch(r.Context(), shared.SessionFrom(r.Context()).UserID, time.Now().In(shared.Location(r))); err != nil {
		slog.Warn("fetching connections", "error", err)
		h.renderConnections(w, r, http.StatusBadGateway, connectionsView{Errors: map[string]string{}, FetchError: i18n.T(r.Context(), "connections.error.fetch")})
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
