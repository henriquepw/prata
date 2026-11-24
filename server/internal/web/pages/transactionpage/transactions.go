// Package transactionpage is the transaction sheet, and the old transactions list that sends to the Mês.
package transactionpage

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/entry"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/oklog/ulid/v2"
)

// Page is the transactions' handlers: the transaction sheet.
type Page struct {
	db *store.Store
}

// New returns the transactions page over db.
func New(db *store.Store) Page {
	return Page{db: db}
}

// NewSheet is an empty transaction sheet for today, in the category the Mês it is opened from is filtered by.
func (p Page) NewSheet(w http.ResponseWriter, r *http.Request) {
	fm, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	fm.Kind, fm.OccurredAt = "OUTCOME", time.Now().In(session.Location(r)).Format(time.DateOnly)
	if current, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && form.HasCategory(fm.Categories, current.Query().Get("category")) {
		fm.CategoryID = current.Query().Get("category")
	}

	session.Render(w, r, http.StatusOK, transactionSheet(fm))
}

// Edit is the sheet of a transaction.
func (p Page) Edit(w http.ResponseWriter, r *http.Request) {
	transaction, ok := p.transaction(w, r)
	if !ok {
		return
	}

	fm, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, transactionSheet(storedTransactionForm(r.Context(), fm, transaction, session.Location(r))))
}

// Save creates the transaction, or updates the one in the path, from the sheet's form. The date is
// local midnight in the user's time zone, stored in UTC. A category or tag left as displayed is not written, so a
// stored reference to a deleted one, displayed as empty, survives editing other fields.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	fm, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	fm.ID = r.PathValue("id")
	fm.Description = strings.TrimSpace(r.PostFormValue("description"))
	fm.Kind = r.PostFormValue("kind")
	fm.Amount = strings.TrimSpace(r.PostFormValue("amount"))
	fm.OccurredAt = r.PostFormValue("occurred_at")
	fm.CategoryID = r.PostFormValue("category_id")
	fm.TagID = r.PostFormValue("tag_id")

	amount, occurredAt := fm.Parse(r)
	if fm.TagID != "" && !form.HasTag(fm.Tags, fm.TagID) {
		fm.Errors["tag_id"] = i18n.T(r.Context(), "form.error.tag")
	}

	change := store.Change{Entity: "TRANSACTION", EntityID: fm.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": form.OrNil(fm.Description), "kind": fm.Kind, "amount": amount, "occurred_at": occurredAt,
		"category_id": form.OrNil(fm.CategoryID), "tag_id": form.OrNil(fm.TagID),
	}}
	if fm.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else {
		stored, ok := p.transaction(w, r)
		if !ok {
			return
		}

		displayed := storedTransactionForm(r.Context(), fm, stored, session.Location(r))
		if fm.CategoryID == displayed.CategoryID {
			delete(change.Fields, "category_id")
		}

		if fm.TagID == displayed.TagID {
			delete(change.Fields, "tag_id")
		}
	}

	session.Commit(p.db, w, r, fm.Errors, transactionSheet(fm), change)
}

// Delete tombstones the transaction in the path.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	transaction, ok := p.transaction(w, r)
	if !ok {
		return
	}

	fm, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	fm = storedTransactionForm(r.Context(), fm, transaction, session.Location(r))
	session.Commit(p.db, w, r, fm.Errors, transactionSheet(fm), store.Change{Entity: "TRANSACTION", EntityID: transaction.ID, Kind: store.KindDelete})
}

// storedTransactionForm fills the sheet with the stored transaction as displayed: its date in loc, and its category
// and tag only when they are among the sheet's live ones.
func storedTransactionForm(ctx context.Context, fm entry.Form, transaction store.Transaction, loc *time.Location) entry.Form {
	fm.ID = transaction.ID
	fm.Description = form.OrEmpty(transaction.Description)
	fm.Kind = transaction.Kind
	fm.Amount = prefs.Amount(ctx, transaction.Amount)
	fm.OccurredAt = prefs.FormatDate(transaction.OccurredAt, loc, time.DateOnly)
	fm.CategoryID, fm.TagID = "", ""
	if id := form.OrEmpty(transaction.CategoryID); form.HasCategory(fm.Categories, id) {
		fm.CategoryID = id
	}

	if id := form.OrEmpty(transaction.TagID); form.HasTag(fm.Tags, id) {
		fm.TagID = id
	}

	return fm
}

// transaction reads the live transaction in the path, answering 404 when the account has none.
func (p Page) transaction(w http.ResponseWriter, r *http.Request) (store.Transaction, bool) {
	transaction, err := p.db.Transaction(r.Context(), session.From(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Transaction{}, false
	}

	if err != nil {
		session.InternalError(w, "reading transaction", err)
		return store.Transaction{}, false
	}

	return transaction, true
}

// Redirect sends the old transactions list to the Mês, keeping its month.
func (p Page) Redirect(w http.ResponseWriter, r *http.Request) {
	url := "/"
	if month := r.URL.Query().Get("month"); month != "" {
		url += "?month=" + month
	}

	session.Redirect(w, r, url)
}
