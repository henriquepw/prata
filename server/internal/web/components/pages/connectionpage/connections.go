// Package connectionpage is the Connections screen: the Pluggy Items feeding the user's accounts.
package connectionpage

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/openfinance"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/view/layout"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/pkg/pluggy"
)

// Page is the Connections handlers.
type Page struct {
	db          *store.Store
	shell       layout.Func
	openFinance openfinance.Config
}

// New returns the connections page over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell layout.Func, openFinance openfinance.Config) Page {
	return Page{db: db, shell: shell, openFinance: openFinance}
}

// connectionsView is the Connections screen: the user's Connections, the accounts to feed, whether the user has Pluggy
// credentials, the typed form with its field errors, and the error of the last "fetch now".
type connectionsView struct {
	Connections    []store.Connection
	Accounts       []store.AccountRef
	HasCredentials bool
	ItemID         string
	AccountID      string
	Errors         map[string]string
	FetchError     string
}

// Connections is the Connections screen.
func (p Page) Connections(w http.ResponseWriter, r *http.Request) {
	p.renderConnections(w, r, http.StatusOK, connectionsView{Errors: map[string]string{}})
}

func (p Page) renderConnections(w http.ResponseWriter, r *http.Request, status int, view connectionsView) {
	if !p.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	sess := session.From(r.Context())

	var err error
	if view.Connections, err = p.db.Connections(r.Context(), sess.UserID); err != nil {
		session.InternalError(w, "listing connections", err)
		return
	}

	if view.Accounts, err = p.db.UserAccounts(r.Context(), sess.UserID); err != nil {
		session.InternalError(w, "listing accounts", err)
		return
	}

	_, err = p.db.PluggyCredentials(r.Context(), p.openFinance.Key, sess.UserID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		session.InternalError(w, "reading pluggy credentials", err)
		return
	}

	view.HasCredentials = err == nil

	s, ok := p.shell(w, r, "nav.connections")
	if !ok {
		return
	}

	session.Render(w, r, status, connectionsPage(s, view))
}

// Save registers the Pluggy Item on the account after Pluggy confirms the Item exists for the user's
// credentials, and reloads the page, or answers 422 with the page and stores nothing when the Item is unknown or
// unreachable or the account is not BRL.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	if !p.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	sess := session.From(r.Context())
	view := connectionsView{ItemID: strings.TrimSpace(r.PostFormValue("item_id")), AccountID: r.PostFormValue("account_id"), Errors: map[string]string{}}

	item, err := p.fetchItem(r, sess.UserID, view.ItemID)
	switch {
	case errors.Is(err, pluggy.ErrItemNotFound):
		view.Errors["item_id"] = l10n.T(r.Context(), "connections.error.item")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, pluggy.ErrInvalidCredentials):
		view.Errors["item_id"] = l10n.T(r.Context(), "settings.error.pluggy_rejected")
	case err != nil:
		slog.Warn("reading pluggy item", "error", err)
		view.Errors["item_id"] = l10n.T(r.Context(), "settings.error.pluggy_offline")
	}

	if len(view.Errors) == 0 {
		err = p.db.AddConnection(r.Context(), sess.UserID, view.AccountID, view.ItemID, item)
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, store.ErrCurrency):
			view.Errors["account_id"] = l10n.T(r.Context(), "connections.error.currency")
		case err != nil:
			session.InternalError(w, "adding connection", err)
			return
		}
	}

	if len(view.Errors) > 0 {
		p.renderConnections(w, r, http.StatusUnprocessableEntity, view)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// fetchItem reads the Pluggy Item with the user's saved credentials, or store.ErrNotFound when there are none.
func (p Page) fetchItem(r *http.Request, userID, itemID string) (pluggy.Item, error) {
	credentials, err := p.db.PluggyCredentials(r.Context(), p.openFinance.Key, userID)
	if err != nil {
		return pluggy.Item{}, err
	}

	apiKey, err := p.openFinance.Pluggy.Authenticate(r.Context(), credentials.ClientID, credentials.ClientSecret)
	if err != nil {
		return pluggy.Item{}, err
	}

	return p.openFinance.Pluggy.Item(r.Context(), apiKey, itemID)
}

// Delete removes the user's Connection and its pending movements, without calling Pluggy.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	if !p.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	err := p.db.DeleteConnection(r.Context(), session.From(r.Context()).UserID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}

	if err != nil {
		session.InternalError(w, "deleting connection", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// Fetch fetches the movements of the user's Connections now, then reloads the screen. When a Connection fails,
// the others are still fetched, the error is logged and the screen answers 502 with a warning over the Connections, whose
// state shows any Item that asks to reconnect.
func (p Page) Fetch(w http.ResponseWriter, r *http.Request) {
	if !p.openFinance.Enabled() {
		http.NotFound(w, r)
		return
	}

	fetcher := openfinance.Fetcher{DB: p.db, Config: p.openFinance}
	if err := fetcher.Fetch(r.Context(), session.From(r.Context()).UserID, time.Now().In(session.Location(r))); err != nil {
		slog.Warn("fetching connections", "error", err)
		p.renderConnections(w, r, http.StatusBadGateway, connectionsView{Errors: map[string]string{}, FetchError: l10n.T(r.Context(), "connections.error.fetch")})
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
