package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/oklog/ulid/v2"
)

// newTransaction is an empty transaction sheet for today, in the category the Mês it is opened from is filtered by.
func (h handler) newTransaction(w http.ResponseWriter, r *http.Request) {
	form, ok := h.transactionForm(w, r)
	if !ok {
		return
	}

	form.Kind, form.OccurredAt = "OUTCOME", time.Now().In(shared.Location(r)).Format(time.DateOnly)
	if current, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && shared.HasCategory(form.Categories, current.Query().Get("category")) {
		form.CategoryID = current.Query().Get("category")
	}

	shared.Render(w, r, http.StatusOK, transactionSheet(form))
}

func (h handler) editTransaction(w http.ResponseWriter, r *http.Request) {
	transaction, ok := h.transaction(w, r)
	if !ok {
		return
	}

	form, ok := h.transactionForm(w, r)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, transactionSheet(storedTransactionForm(r.Context(), form, transaction, shared.Location(r))))
}

// saveTransaction creates the transaction, or updates the one in the path, from the sheet's form. The date is
// local midnight in the user's time zone, stored in UTC. A category or tag left as displayed is not written, so a
// stored reference to a deleted one, displayed as empty, survives editing other fields.
func (h handler) saveTransaction(w http.ResponseWriter, r *http.Request) {
	form, ok := h.transactionForm(w, r)
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

	amount := shared.ParseEntry(r.Context(), form.Errors, form.Amount, form.Kind)
	occurredAt, ok := shared.ParseDate(form.OccurredAt, shared.Location(r))
	if !ok {
		form.Errors["occurred_at"] = i18n.T(r.Context(), "form.error.date")
	}

	if form.CategoryID != "" && !shared.HasCategory(form.Categories, form.CategoryID) {
		form.Errors["category_id"] = i18n.T(r.Context(), "form.error.category")
	}

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
		stored, ok := h.transaction(w, r)
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

	h.commit(w, r, form.Errors, transactionSheet(form), change)
}

func (h handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	transaction, ok := h.transaction(w, r)
	if !ok {
		return
	}

	form, ok := h.transactionForm(w, r)
	if !ok {
		return
	}

	form = storedTransactionForm(r.Context(), form, transaction, shared.Location(r))
	h.commit(w, r, form.Errors, transactionSheet(form), store.Change{Entity: "TRANSACTION", EntityID: transaction.ID, Kind: store.KindDelete})
}

// transactionForm is an empty sheet with the live categories and tags to pick from, answering 500 when it
// cannot read them.
func (h handler) transactionForm(w http.ResponseWriter, r *http.Request) (transactionForm, bool) {
	categories, ok := shared.LiveCategories(w, r, h.db)
	if !ok {
		return transactionForm{}, false
	}

	tags, ok := shared.LiveTags(w, r, h.db)
	if !ok {
		return transactionForm{}, false
	}

	return transactionForm{Categories: categories, Tags: tags, Errors: map[string]string{}}, true
}

// storedTransactionForm fills the sheet with the stored transaction as displayed: its date in loc, and its category
// and tag only when they are among the sheet's live ones.
func storedTransactionForm(ctx context.Context, form transactionForm, transaction store.Transaction, loc *time.Location) transactionForm {
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
func (h handler) transaction(w http.ResponseWriter, r *http.Request) (store.Transaction, bool) {
	transaction, err := h.db.Transaction(r.Context(), shared.SessionFrom(r.Context()).AccountID, r.PathValue("id"))
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
