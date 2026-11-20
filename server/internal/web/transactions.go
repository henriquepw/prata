package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

const monthLayout = "2006-01"

// monthWindow is the month of t, as its first day at midnight in t's location and as [from, to) in RFC 3339 UTC.
func monthWindow(t time.Time) (month time.Time, from, to string) {
	month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())

	return month, month.UTC().Format(time.RFC3339), month.AddDate(0, 1, 0).UTC().Format(time.RFC3339)
}

// transactions lists the month in ?month=YYYY-MM, the current one in the user's time zone by default, beside the
// activity log.
func (h handler) transactions(w http.ResponseWriter, r *http.Request) {
	loc, accountID := location(r), sessionFrom(r.Context()).AccountID
	month, err := time.ParseInLocation(monthLayout, r.URL.Query().Get("month"), loc)
	if err != nil {
		month = time.Now().In(loc)
	}

	month, from, to := monthWindow(month)
	transactions, err := h.db.Transactions(r.Context(), accountID, from, to)
	if err != nil {
		internalError(w, "listing transactions", err)
		return
	}

	activity, err := h.db.Activity(r.Context(), accountID)
	if err != nil {
		internalError(w, "reading activity", err)
		return
	}

	s, ok := h.shell(w, r, "nav.transactions")
	if !ok {
		return
	}

	render(w, r, http.StatusOK, transactionsPage(s, month, transactions, activity))
}

func (h handler) newTransaction(w http.ResponseWriter, r *http.Request) {
	form, ok := h.transactionForm(w, r)
	if !ok {
		return
	}

	form.Kind, form.OccurredAt = "OUTCOME", time.Now().In(location(r)).Format(time.DateOnly)
	render(w, r, http.StatusOK, transactionSheet(form))
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

	render(w, r, http.StatusOK, transactionSheet(storedTransactionForm(r.Context(), form, transaction, location(r))))
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

	amount := parseEntry(r.Context(), form.Errors, form.Amount, form.Kind)
	occurredAt, ok := parseDate(form.OccurredAt, location(r))
	if !ok {
		form.Errors["occurred_at"] = i18n.T(r.Context(), "form.error.date")
	}

	if form.CategoryID != "" && !hasCategory(form.Categories, form.CategoryID) {
		form.Errors["category_id"] = i18n.T(r.Context(), "form.error.category")
	}

	if form.TagID != "" && !hasTag(form.Tags, form.TagID) {
		form.Errors["tag_id"] = i18n.T(r.Context(), "form.error.tag")
	}

	change := store.Change{Entity: "TRANSACTION", EntityID: form.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": orNil(form.Description), "kind": form.Kind, "amount": amount, "occurred_at": occurredAt,
		"category_id": orNil(form.CategoryID), "tag_id": orNil(form.TagID),
	}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else {
		stored, ok := h.transaction(w, r)
		if !ok {
			return
		}

		displayed := storedTransactionForm(r.Context(), form, stored, location(r))
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

	form = storedTransactionForm(r.Context(), form, transaction, location(r))
	h.commit(w, r, form.Errors, transactionSheet(form), store.Change{Entity: "TRANSACTION", EntityID: transaction.ID, Kind: store.KindDelete})
}

// transactionForm is an empty sheet with the live categories and tags to pick from, answering 500 when it
// cannot read them.
func (h handler) transactionForm(w http.ResponseWriter, r *http.Request) (transactionForm, bool) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return transactionForm{}, false
	}

	tags, ok := h.liveTags(w, r)
	if !ok {
		return transactionForm{}, false
	}

	return transactionForm{Categories: categories, Tags: tags, Errors: map[string]string{}}, true
}

// storedTransactionForm fills the sheet with the stored transaction as displayed: its date in loc, and its category
// and tag only when they are among the sheet's live ones.
func storedTransactionForm(ctx context.Context, form transactionForm, transaction store.Transaction, loc *time.Location) transactionForm {
	form.ID = transaction.ID
	form.Description = orEmpty(transaction.Description)
	form.Kind = transaction.Kind
	form.Amount = amount(ctx, transaction.Amount)
	form.OccurredAt = formatDate(transaction.OccurredAt, loc, time.DateOnly)
	form.CategoryID, form.TagID = "", ""
	if id := orEmpty(transaction.CategoryID); hasCategory(form.Categories, id) {
		form.CategoryID = id
	}

	if id := orEmpty(transaction.TagID); hasTag(form.Tags, id) {
		form.TagID = id
	}

	return form
}

// transaction reads the live transaction in the path, answering 404 when the account has none.
func (h handler) transaction(w http.ResponseWriter, r *http.Request) (store.Transaction, bool) {
	transaction, err := h.db.Transaction(r.Context(), sessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Transaction{}, false
	}

	if err != nil {
		internalError(w, "reading transaction", err)
		return store.Transaction{}, false
	}

	return transaction, true
}
