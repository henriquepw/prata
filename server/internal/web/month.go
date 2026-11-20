package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/utils"
	"github.com/henriquepw/prata/shared/recurrence"
	"github.com/oklog/ulid/v2"
)

const monthLayout = "2006-01"

// monthWindow is the month of t, as its first day at midnight in t's location and as [from, to) in RFC 3339 UTC.
func monthWindow(t time.Time) (month time.Time, from, to string) {
	month = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())

	return month, month.UTC().Format(time.RFC3339), month.AddDate(0, 1, 0).UTC().Format(time.RFC3339)
}

// balance is what the ticker shows: the account's realized balance, the occurrences still pending up to the end of
// this month and the projected balance, which is the realized one plus every pending occurrence.
type balance struct {
	Realized, Projected int64
	Pending             []occurrence
}

// occurrence is a pending occurrence of a recurrence, Overdue when its date is before today.
type occurrence struct {
	Recurrence store.Recurrence
	Date       string
	Overdue    bool
}

func (b balance) overdue() int {
	count := 0
	for _, pending := range b.Pending {
		if pending.Overdue {
			count++
		}
	}

	return count
}

// balance reads the realized balance and the occurrences pending up to the end of now's month, in now's location.
func (h handler) balance(ctx context.Context, accountID string, now time.Time) (balance, error) {
	realized, err := h.db.RealizedBalance(ctx, accountID)
	if err != nil {
		return balance{}, fmt.Errorf("reading realized balance: %w", err)
	}

	month, _, _ := monthWindow(now)
	pending, err := h.pending(ctx, accountID, now, month.AddDate(0, 1, -1))
	if err != nil {
		return balance{}, err
	}

	b := balance{Realized: realized, Projected: realized, Pending: pending}
	for _, p := range pending {
		b.Projected += signed(p.Recurrence.Kind, p.Recurrence.Amount)
	}

	return b, nil
}

// pending lists by date the occurrences from each live recurrence's start_at through the day of end, minus the
// confirmed ones, overdue before the day of now.
func (h handler) pending(ctx context.Context, accountID string, now, end time.Time) ([]occurrence, error) {
	recurrences, err := h.db.Recurrences(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("listing recurrences: %w", err)
	}

	confirmed, err := h.db.ConfirmedOccurrences(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("listing confirmed occurrences: %w", err)
	}

	today, pending := now.Format(time.DateOnly), []occurrence{}
	for _, rec := range recurrences {
		dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, rec.StartAt, end.Format(time.DateOnly))
		if err != nil {
			return nil, fmt.Errorf("expanding recurrence: %w", err)
		}

		for _, date := range dates {
			if !slices.Contains(confirmed[rec.ID], date) {
				pending = append(pending, occurrence{Recurrence: rec, Date: date, Overdue: date < today})
			}
		}
	}

	slices.SortStableFunc(pending, func(a, b occurrence) int { return cmp.Compare(a.Date, b.Date) })

	return pending, nil
}

// month is the Mês screen: the boxes of the month shown, in cents, its statement by day, newest first, and the usage of
// each category. Overdue holds, on the current month only, the occurrences of earlier months still pending, drawn under
// the "hoje" line, which sits before Days[TodayAt] (after the last day when TodayAt is len(Days), nowhere when it is
// -1). Filter is the category the statement is narrowed to, empty when it shows all; the boxes never are. Flash is the
// row to flash, Error a rejected confirmation and Focus puts the prompt in focus, as after a launch.
type month struct {
	Month                              time.Time
	Today                              string
	Previous, Income, Outcome, Pending int64
	Days                               []day
	Overdue                            []occurrence
	TodayAt                            int
	Usages                             []store.CategoryUsage
	Filter                             store.CategoryUsage
	Flash, Error                       string
	Focus                              bool
}

// day is one day of the statement, as YYYY-MM-DD: its pending occurrences, its transactions and their Total.
type day struct {
	Date         string
	Total        int64
	Pending      []occurrence
	Transactions []store.Transaction
}

func (m month) isCurrent() bool { return m.Month.Format(monthLayout) == m.Today[:len(monthLayout)] }

func (m month) realized() int64 { return m.Previous + m.Income - m.Outcome }

// shows reports whether the statement lists an entry of categoryID under the month's filter.
func (m month) shows(categoryID *string) bool {
	return m.Filter.ID == "" || orEmpty(categoryID) == m.Filter.ID
}

// readMonth reads the month of shown in now's location: the balance before it, its transactions and usage, and the
// occurrences pending in it plus, on the current month, the overdue ones of earlier months. The statement lists only
// the entries of category when it is one of the live categories.
func (h handler) readMonth(ctx context.Context, accountID string, now, shown time.Time, category string) (month, error) {
	first, from, to := monthWindow(shown)
	last := first.AddDate(0, 1, -1)
	page := month{Month: first, Today: now.Format(time.DateOnly)}
	previous, err := h.db.BalanceBefore(ctx, accountID, from)
	if err != nil {
		return month{}, fmt.Errorf("reading the balance before the month: %w", err)
	}

	transactions, err := h.db.Transactions(ctx, accountID, from, to)
	if err != nil {
		return month{}, fmt.Errorf("listing transactions: %w", err)
	}

	page.Usages, err = h.db.CategoryUsages(ctx, accountID, from, to)
	if err != nil {
		return month{}, fmt.Errorf("reading category usage: %w", err)
	}

	if i := slices.IndexFunc(page.Usages, func(u store.CategoryUsage) bool { return u.ID == category }); i >= 0 {
		page.Filter = page.Usages[i]
	}

	pending, err := h.pending(ctx, accountID, now, last)
	if err != nil {
		return month{}, err
	}

	page.Previous = previous
	page.Income, page.Outcome = totals(transactions)
	days := map[string]*day{}
	at := func(date string) *day {
		if days[date] == nil {
			days[date] = &day{Date: date}
		}

		return days[date]
	}

	for _, p := range pending {
		isEarlier := p.Date < first.Format(time.DateOnly)
		if isEarlier && !page.isCurrent() {
			continue
		}

		page.Pending += signed(p.Recurrence.Kind, p.Recurrence.Amount)
		switch {
		case !page.shows(p.Recurrence.CategoryID):
			continue
		case isEarlier:
			page.Overdue = append(page.Overdue, p)
		default:
			at(p.Date).Pending = append(at(p.Date).Pending, p)
		}
	}

	for _, t := range transactions {
		if !page.shows(t.CategoryID) {
			continue
		}

		d := at(formatDate(t.OccurredAt, now.Location(), time.DateOnly))
		d.Transactions = append(d.Transactions, t)
		d.Total += signed(t.Kind, t.Amount)
	}

	for _, d := range days {
		page.Days = append(page.Days, *d)
	}

	slices.SortFunc(page.Days, func(a, b day) int { return cmp.Compare(b.Date, a.Date) })
	page.TodayAt = -1
	if page.isCurrent() {
		page.TodayAt = len(page.Days)
		if i := slices.IndexFunc(page.Days, func(d day) bool { return d.Date <= page.Today }); i >= 0 {
			page.TodayAt = i
		}
	}

	return page, nil
}

// shownMonth is the month in ?month=YYYY-MM, or the current one, in now's location.
func shownMonth(r *http.Request, now time.Time) time.Time {
	shown, err := time.ParseInLocation(monthLayout, r.FormValue("month"), now.Location())
	if err != nil {
		return now
	}

	return shown
}

// home is the Mês screen of ?month=YYYY-MM, the current month in the user's time zone by default, with the statement
// narrowed to ?category=<id>.
func (h handler) home(w http.ResponseWriter, r *http.Request) {
	s, ok := h.shell(w, r, "nav.month")
	if !ok {
		return
	}

	page, err := h.readMonth(r.Context(), sessionFrom(r.Context()).AccountID, s.Now, shownMonth(r, s.Now), r.FormValue("category"))
	if err != nil {
		internalError(w, "reading month", err)
		return
	}

	page.Flash = s.Flash
	render(w, r, http.StatusOK, monthPage(s, page))
}

// transactions sends the old transactions list to the Mês, keeping its month.
func (h handler) transactions(w http.ResponseWriter, r *http.Request) {
	url := "/"
	if month := r.URL.Query().Get("month"); month != "" {
		url += "?month=" + month
	}

	redirect(w, r, url)
}

// occurrenceForm is the "confirmar ocorrência" sheet: the transaction sheet filled from the recurrence and the
// occurrence's date.
type occurrenceForm struct {
	transactionForm
	Recurrence store.Recurrence
	Date       string
}

// occurrence reads the recurrence and the date of the occurrence in the path, answering 404 when the recurrence has
// no occurrence on that date, and fills the sheet from them, with the recurrence's category only while it is live.
func (h handler) occurrence(w http.ResponseWriter, r *http.Request) (occurrenceForm, bool) {
	rec, ok := h.recurrence(w, r)
	if !ok {
		return occurrenceForm{}, false
	}

	date := r.PathValue("date")
	dates, err := recurrence.ExpandRule(rec.Rule, rec.StartAt, rec.EndAt, date, date)
	if err != nil || len(dates) == 0 {
		http.NotFound(w, r)
		return occurrenceForm{}, false
	}

	form, ok := h.transactionForm(w, r)
	if !ok {
		return occurrenceForm{}, false
	}

	form.Kind, form.Amount, form.Description, form.OccurredAt = rec.Kind, amount(r.Context(), rec.Amount), orEmpty(rec.Description), date
	if id := orEmpty(rec.CategoryID); hasCategory(form.Categories, id) {
		form.CategoryID = id
	}

	return occurrenceForm{transactionForm: form, Recurrence: rec, Date: date}, true
}

func (h handler) occurrenceSheet(w http.ResponseWriter, r *http.Request) {
	form, ok := h.occurrence(w, r)
	if !ok {
		return
	}

	render(w, r, http.StatusOK, occurrenceSheet(form))
}

// confirmOccurrence turns the occurrence in the path into a transaction at local midnight of its date in UTC. The
// sheet posts what the user adjusted and gets the sheet's answer; the statement's row posts nothing but the month and
// the filter on screen and gets that month back with the new row flashing. On the row, an occurrence already confirmed
// elsewhere hits the unique (recurrence_id, occurrence_date) and answers 409 with "já confirmada"; any other rejection
// is a 500.
func (h handler) confirmOccurrence(w http.ResponseWriter, r *http.Request) {
	form, ok := h.occurrence(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	isSheet := r.PostForm.Has("amount")
	if isSheet {
		form.Kind = r.PostFormValue("kind")
		form.Amount = strings.TrimSpace(r.PostFormValue("amount"))
		form.Description = strings.TrimSpace(r.PostFormValue("description"))
		form.OccurredAt = r.PostFormValue("occurred_at")
		form.CategoryID = r.PostFormValue("category_id")
	}

	cents := parseEntry(r.Context(), form.Errors, form.Amount, form.Kind)
	occurredAt, ok := parseDate(form.OccurredAt, location(r))
	if !ok {
		form.Errors["occurred_at"] = i18n.T(r.Context(), "form.error.date")
	}

	if form.CategoryID != "" && !hasCategory(form.Categories, form.CategoryID) {
		form.Errors["category_id"] = i18n.T(r.Context(), "form.error.category")
	}

	change := store.Change{Entity: "TRANSACTION", EntityID: ulid.Make().String(), Kind: store.KindCreate, Fields: map[string]any{
		"kind": form.Kind, "amount": cents, "category_id": orNil(form.CategoryID), "description": orNil(form.Description),
		"recurrence_id": form.Recurrence.ID, "occurrence_date": form.Date, "occurred_at": occurredAt,
	}}
	if isSheet {
		h.commit(w, r, form.Errors, occurrenceSheet(form), change)
		return
	}

	h.createInMonth(w, r, change, func() (string, error) {
		confirmed, err := h.db.ConfirmedOccurrences(r.Context(), sessionFrom(r.Context()).AccountID)
		if err != nil || !slices.Contains(confirmed[form.Recurrence.ID], form.Date) {
			return "", err
		}

		return i18n.T(r.Context(), "month.error.confirmed"), nil
	})
}

// createInMonth commits change, a new transaction, and answers the month on screen with its row flashing. A rejection
// answers 409 with the message conflict reads, or 500 when conflict is nil or reads none.
func (h handler) createInMonth(w http.ResponseWriter, r *http.Request, change store.Change, conflict func() (string, error)) {
	reason, err := h.db.CommitChanges(r.Context(), sessionFrom(r.Context()), change)
	message := ""
	if err == nil && reason != "" && conflict != nil {
		message, err = conflict()
	}

	if err == nil && reason != "" && message == "" {
		err = fmt.Errorf("rejected: %s", reason)
	}

	if err != nil {
		internalError(w, "creating transaction", err)
		return
	}

	h.renderMonth(w, r, utils.IfElse(message == "", http.StatusOK, http.StatusConflict), change.EntityID, message)
}

// renderMonth answers the month on screen, which a write in place swaps, with the row flash flashing and message as
// its error, along with the ticker it changed.
func (h handler) renderMonth(w http.ResponseWriter, r *http.Request, status int, flash, message string) {
	session, now := sessionFrom(r.Context()), time.Now().In(location(r))
	page, err := h.readMonth(r.Context(), session.AccountID, now, shownMonth(r, now), r.FormValue("category"))
	if err != nil {
		internalError(w, "reading month", err)
		return
	}

	balance, err := h.balance(r.Context(), session.AccountID, now)
	if err != nil {
		internalError(w, "reading balance", err)
		return
	}

	page.Flash, page.Error, page.Focus = flash, message, r.URL.Path == "/launch"
	render(w, r, status, monthSwap(shell{Email: session.Email, Balance: balance}, page))
}

// totals is the INCOME and the OUTCOME sums of transactions, in cents.
func totals(transactions []store.Transaction) (income, outcome int64) {
	for _, transaction := range transactions {
		if transaction.Kind == "INCOME" {
			income += transaction.Amount
		} else {
			outcome += transaction.Amount
		}
	}

	return income, outcome
}

// kindClass is the color of an amount of kind: cat-green for INCOME, destructive for OUTCOME.
func kindClass(kind string) string {
	if kind == "INCOME" {
		return "text-cat-green"
	}

	return "text-destructive"
}

// signed is the amount with the kind's sign: positive for INCOME, negative for OUTCOME.
func signed(kind string, amount int64) int64 {
	if kind == "INCOME" {
		return amount
	}

	return -amount
}
