package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/pluggy"
	"github.com/henriquepw/prata/server/internal/store"
)

// connectionsView is the Conexões screen: the user's Conexões, the accounts to feed, whether the user has Pluggy
// credentials, and the typed form with its field errors.
type connectionsView struct {
	Connections    []store.Connection
	Accounts       []store.AccountRef
	HasCredentials bool
	ItemID         string
	AccountID      string
	Errors         map[string]string
}

func (h handler) connections(w http.ResponseWriter, r *http.Request) {
	h.renderConnections(w, r, http.StatusOK, connectionsView{Errors: map[string]string{}})
}

func (h handler) renderConnections(w http.ResponseWriter, r *http.Request, status int, view connectionsView) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	session := sessionFrom(r.Context())

	var err error
	if view.Connections, err = h.db.Connections(r.Context(), session.UserID); err != nil {
		internalError(w, "listing connections", err)
		return
	}

	if view.Accounts, err = h.db.UserAccounts(r.Context(), session.UserID); err != nil {
		internalError(w, "listing accounts", err)
		return
	}

	_, err = h.db.PluggyCredentials(r.Context(), h.openFinance.Key, session.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, "reading pluggy credentials", err)
		return
	}

	view.HasCredentials = err == nil

	s, ok := h.shell(w, r, "nav.connections")
	if !ok {
		return
	}

	render(w, r, status, connectionsPage(s, view))
}

// saveConnection registers the Pluggy Item on the account after Pluggy confirms the Item exists for the user's
// credentials, and reloads the page, or answers 422 with the page and stores nothing when the Item is unknown or
// unreachable or the account is not BRL.
func (h handler) saveConnection(w http.ResponseWriter, r *http.Request) {
	if !h.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	session := sessionFrom(r.Context())
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
		err = h.db.AddConnection(r.Context(), session.UserID, view.AccountID, view.ItemID, item.Institution(), item.Status, item.ExecutionStatus, item.LastUpdatedAt)
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, store.ErrCurrency):
			view.Errors["account_id"] = i18n.T(r.Context(), "connections.error.currency")
		case err != nil:
			internalError(w, "adding connection", err)
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

	err := h.db.DeleteConnection(r.Context(), sessionFrom(r.Context()).UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}

	if err != nil {
		internalError(w, "deleting connection", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
