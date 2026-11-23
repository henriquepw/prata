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
	"github.com/henriquepw/prata/server/internal/web/shared"
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
	form, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	form.Kind, form.OccurredAt = "OUTCOME", time.Now().In(shared.Location(r)).Format(time.DateOnly)
	if current, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && shared.HasCategory(form.Categories, current.Query().Get("category")) {
		form.CategoryID = current.Query().Get("category")
	}

	shared.Render(w, r, http.StatusOK, transactionSheet(form))
}

// Edit is the sheet of a transaction.
func (p Page) Edit(w http.ResponseWriter, r *http.Request) {
	transaction, ok := p.transaction(w, r)
	if !ok {
		return
	}

	form, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, transactionSheet(storedTransactionForm(r.Context(), form, transaction, shared.Location(r))))
}

// Save creates the transaction, or updates the one in the path, from the sheet's form. The date is
// local midnight in the user's time zone, stored in UTC. A category or tag left as displayed is not written, so a
// stored reference to a deleted one, displayed as empty, survives editing other fields.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	form, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	form.ID = r.PathValue("id")
	form.Description = strings.TrimSpace(r.PostFormValue("description"))
	form.Kind = r.PostFormValue("kind")
	form.Amount = strings.TrimSpace(r.PostFormValue("amount"))
	form.OccurredAt = r.PostFormValue("occurred_at")
	form.CategoryID = r.PostFormValue("category_id")
	form.TagID = r.PostFormValue("tag_id")

	amount, occurredAt := form.Parse(r)
	if form.TagID != "" && !shared.HasTag(form.Tags, form.TagID) {
		form.Errors["tag_id"] = i18n.T(r.Context(), "form.error.tag")
	}

	change := store.Change{Entity: "TRANSACTION", EntityID: form.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": shared.OrNil(form.Description), "kind": form.Kind, "amount": amount, "occurred_at": occurredAt,
		"category_id": shared.OrNil(form.CategoryID), "tag_id": shared.OrNil(form.TagID),
	}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else {
		stored, ok := p.transaction(w, r)
		if !ok {
			return
		}

		displayed := storedTransactionForm(r.Context(), form, stored, shared.Location(r))
		if form.CategoryID == displayed.CategoryID {
			delete(change.Fields, "category_id")
		}

		if form.TagID == displayed.TagID {
			delete(change.Fields, "tag_id")
		}
	}

	shared.Commit(p.db, w, r, form.Errors, transactionSheet(form), change)
}

// Delete tombstones the transaction in the path.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	transaction, ok := p.transaction(w, r)
	if !ok {
		return
	}

	form, ok := entry.Load(w, r, p.db)
	if !ok {
		return
	}

	form = storedTransactionForm(r.Context(), form, transaction, shared.Location(r))
	shared.Commit(p.db, w, r, form.Errors, transactionSheet(form), store.Change{Entity: "TRANSACTION", EntityID: transaction.ID, Kind: store.KindDelete})
}

// storedTransactionForm fills the sheet with the stored transaction as displayed: its date in loc, and its category
// and tag only when they are among the sheet's live ones.
func storedTransactionForm(ctx context.Context, form entry.Form, transaction store.Transaction, loc *time.Location) entry.Form {
	form.ID = transaction.ID
	form.Description = shared.OrEmpty(transaction.Description)
	form.Kind = transaction.Kind
	form.Amount = shared.Amount(ctx, transaction.Amount)
	form.OccurredAt = shared.FormatDate(transaction.OccurredAt, loc, time.DateOnly)
	form.CategoryID, form.TagID = "", ""
	if id := shared.OrEmpty(transaction.CategoryID); shared.HasCategory(form.Categories, id) {
		form.CategoryID = id
	}

	if id := shared.OrEmpty(transaction.TagID); shared.HasTag(form.Tags, id) {
		form.TagID = id
	}

	return form
}

// transaction reads the live transaction in the path, answering 404 when the account has none.
func (p Page) transaction(w http.ResponseWriter, r *http.Request) (store.Transaction, bool) {
	transaction, err := p.db.Transaction(r.Context(), shared.SessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Transaction{}, false
	}

	if err != nil {
		shared.InternalError(w, "reading transaction", err)
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

	shared.Redirect(w, r, url)
}
