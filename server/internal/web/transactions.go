package web

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

const monthLayout = "2006-01"

var monthNames = []string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

// transactions lists the month in ?month=YYYY-MM, the current one in the user's time zone by default, as
// [from, to) in UTC.
func (h handler) transactions(w http.ResponseWriter, r *http.Request) {
	loc := location(r)
	month, err := time.ParseInLocation(monthLayout, r.URL.Query().Get("month"), loc)
	if err != nil {
		now := time.Now().In(loc)
		month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	}

	from, to := month.UTC().Format(time.RFC3339), month.AddDate(0, 1, 0).UTC().Format(time.RFC3339)
	transactions, err := h.db.Transactions(r.Context(), sessionFrom(r.Context()).AccountID, from, to)
	if err != nil {
		internalError(w, "listing transactions", err)
		return
	}

	render(w, r, http.StatusOK, transactionsPage(newShell(r, "Transações"), month, transactions, loc))
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

	form, ok := h.storedTransactionForm(w, r, transaction)
	if !ok {
		return
	}

	render(w, r, http.StatusOK, transactionSheet(form))
}

// saveTransaction creates the transaction, or updates the one in the path, from the sheet's form. The date is
// local midnight in the user's time zone, stored in UTC.
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

	amount, ok := parseCents(form.Amount)
	if !ok || amount <= 0 {
		form.Errors["amount"] = "Informe um valor maior que zero."
	}

	if !slices.ContainsFunc(kinds, func(k kind) bool { return k.Value == form.Kind }) {
		form.Errors["kind"] = "Escolha o tipo."
	}

	occurredAt, ok := parseDate(form.OccurredAt, location(r))
	if !ok {
		form.Errors["occurred_at"] = "Informe a data."
	}

	if form.CategoryID != "" && !slices.ContainsFunc(form.Categories, func(c store.Category) bool { return c.ID == form.CategoryID }) {
		form.Errors["category_id"] = "Escolha uma categoria."
	}

	if form.TagID != "" && !slices.ContainsFunc(form.Tags, func(t store.Tag) bool { return t.ID == form.TagID }) {
		form.Errors["tag_id"] = "Escolha uma tag."
	}

	change := store.Change{Entity: "TRANSACTION", EntityID: form.ID, Kind: store.KindUpdate, Fields: map[string]any{
		"description": orNil(form.Description), "kind": form.Kind, "amount": amount, "occurred_at": occurredAt,
		"category_id": orNil(form.CategoryID), "tag_id": orNil(form.TagID),
	}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else if _, ok := h.transaction(w, r); !ok {
		return
	}

	h.commit(w, r, form.Errors, transactionSheet(form), change)
}

func (h handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	transaction, ok := h.transaction(w, r)
	if !ok {
		return
	}

	form, ok := h.storedTransactionForm(w, r, transaction)
	if !ok {
		return
	}

	h.commit(w, r, form.Errors, transactionSheet(form), store.Change{Entity: "TRANSACTION", EntityID: transaction.ID, Kind: store.KindDelete})
}

// transactionForm is an empty sheet with the live categories and tags to pick from, answering 500 when it
// cannot read them.
func (h handler) transactionForm(w http.ResponseWriter, r *http.Request) (transactionForm, bool) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return transactionForm{}, false
	}

	tags, err := h.db.Tags(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing tags", err)
		return transactionForm{}, false
	}

	return transactionForm{Categories: categories, Tags: tags, Errors: map[string]string{}}, true
}

// storedTransactionForm fills the sheet with the stored transaction, its date in the user's time zone.
func (h handler) storedTransactionForm(w http.ResponseWriter, r *http.Request, transaction store.Transaction) (transactionForm, bool) {
	form, ok := h.transactionForm(w, r)
	if !ok {
		return form, false
	}

	form.ID = transaction.ID
	form.Description = orEmpty(transaction.Description)
	form.Kind = transaction.Kind
	form.Amount = formatCents(transaction.Amount)
	form.OccurredAt = formatDate(transaction.OccurredAt, location(r), time.DateOnly)
	form.CategoryID = orEmpty(transaction.CategoryID)
	form.TagID = orEmpty(transaction.TagID)

	return form, true
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
