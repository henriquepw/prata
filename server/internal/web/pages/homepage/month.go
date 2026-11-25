// Package homepage is the Month screen and its launch prompt. A page package follows this pattern: New takes what the
// page depends on and returns a Page whose methods are http.HandlerFuncs; web wires them to routes and middleware.
package homepage

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/internal/web/utils"
)

// Page is the homepage's handlers: the Month screen and the launch prompt.
type Page struct {
	db    *store.Store
	shell shell.Func
}

// New returns the homepage over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell shell.Func) Page {
	return Page{db: db, shell: shell}
}

const monthLayout = "2006-01"

// month is the Month screen: the boxes of the month shown, in cents, its statement by day, newest first, and the usage of
// each category. Overdue holds, on the current month only, the occurrences of earlier months still pending, drawn under
// the "hoje" line, which sits before Days[TodayAt] (after the last day when TodayAt is len(Days), nowhere when it is
// -1). Filter is the category the statement is narrowed to, empty when it shows all; the boxes never are. Flash is the
// row to flash, Error a rejected confirmation and Focus puts the prompt in focus, as after a launch.
type month struct {
	Month                              time.Time
	Today                              string
	Previous, Income, Outcome, Pending int64
	Days                               []day
	Overdue                            []shell.Occurrence
	TodayAt                            int
	Usages                             []store.CategoryUsage
	Daily                              daily
	Filter                             store.CategoryUsage
	Flash, Error                       string
	Focus                              bool
	Limit                              int
	More                               bool
}

// statementPage is how many entries the statement adds each time it opens, and shows at first.
const statementPage = 20

// day is one day of the statement, as YYYY-MM-DD: its pending occurrences, its transactions and their Total.
type day struct {
	Date         string
	Total        int64
	Pending      []shell.Occurrence
	Transactions []store.Transaction
}

// daily is the daily limit panel: a cell per day of the month, Lead blank cells before the first to line it up on its
// weekday, the daily limit mode and the Budget of the daily categories it spreads, all empty (and Defined false)
// while the account has no monthly income.
type daily struct {
	Defined bool
	Lead    int
	Cells   []dailyCell
	Mode    string
	Budget  int64
}

// dailyCell is a day of the month as YYYY-MM-DD: the Spent on the daily categories, the Limit and the Tone of it, one
// of gray, green, yellow or red.
type dailyCell struct {
	Date         string
	Day          int
	Spent, Limit int64
	Tone         string
}

// readDaily is the daily limit of each day of first's month in the session's daily limit mode, empty without a
// monthly income: fixed, the monthly limits of the daily categories over the days of the month; dynamic, what is left
// of them after the spending before the day over the days left counting it, never below zero, kept from today on. Each is set against what transactions spent on the
// daily categories on that day in the location of first. A day is gray when it is after today or spent nothing, else
// green up to 80% of the limit, yellow up to 100% and red above it.
func readDaily(sess store.Session, first time.Time, today string, usages []store.CategoryUsage, transactions []store.Transaction) daily {
	if sess.MonthlyIncome == 0 {
		return daily{}
	}

	isDaily, dailyBudget := map[string]bool{}, int64(0)

	for _, usage := range usages {
		if usage.IsDaily {
			isDaily[usage.ID], dailyBudget = true, dailyBudget+usage.MonthlyLimit
		}
	}

	spent := map[string]int64{}
	for _, t := range transactions {
		if t.Kind == "OUTCOME" && isDaily[form.OrEmpty(t.CategoryID)] {
			spent[prefs.FormatDate(t.OccurredAt, first.Location(), time.DateOnly)] += t.Amount
		}
	}

	days := first.AddDate(0, 1, -1).Day()
	limit, spentBefore := dailyBudget/int64(days), int64(0)
	d := daily{Defined: true, Lead: int(first.Weekday()), Mode: sess.DailyLimitMode, Budget: dailyBudget}

	for i := range days {
		date := first.AddDate(0, 0, i).Format(time.DateOnly)
		if sess.DailyLimitMode == prefs.DailyLimitDynamic && (i == 0 || date <= today) {
			limit = max(0, dailyBudget-spentBefore) / int64(days-i)
		}

		cell := dailyCell{Date: date, Day: i + 1, Spent: spent[date], Limit: limit, Tone: "gray"}
		switch {
		case date > today || cell.Spent == 0:
		case cell.Spent*100 <= limit*80:
			cell.Tone = "green"
		case cell.Spent <= limit:
			cell.Tone = "yellow"
		default:
			cell.Tone = "red"
		}

		spentBefore += cell.Spent
		d.Cells = append(d.Cells, cell)
	}

	return d
}

// percent is the rounded share of the Limit the day spent, 0 with no limit.
func (c dailyCell) percent() int64 {
	if c.Limit == 0 {
		return 0
	}

	return (c.Spent*100 + c.Limit/2) / c.Limit
}

// yearMonth is the month shown, as YYYY-MM.
func (m month) yearMonth() string { return m.Month.Format(monthLayout) }

func (m month) isCurrent() bool { return m.yearMonth() == m.Today[:len(monthLayout)] }

func (m month) realized() int64 { return m.Previous + m.Income - m.Outcome }

// shows reports whether the statement lists an entry of categoryID under the month's filter.
func (m month) shows(categoryID *string) bool {
	return m.Filter.ID == "" || form.OrEmpty(categoryID) == m.Filter.ID
}

// readMonth reads the month of shown in now's location: the balance before it, its transactions and usage, and the
// occurrences pending in it plus, on the current month, the overdue ones of earlier months. The statement lists only
// the entries of category when it is one of the live categories.
func (p Page) readMonth(ctx context.Context, accountID string, now, shown time.Time, category string, limit int) (month, error) {
	first, from, to := form.MonthWindow(shown)
	last := first.AddDate(0, 1, -1)
	page := month{Month: first, Today: now.Format(time.DateOnly), Limit: limit}
	previous, err := p.db.BalanceBefore(ctx, accountID, from)
	if err != nil {
		return month{}, fmt.Errorf("reading the balance before the month: %w", err)
	}

	transactions, err := p.db.Transactions(ctx, accountID, from, to)
	if err != nil {
		return month{}, fmt.Errorf("listing transactions: %w", err)
	}

	page.Usages, err = p.db.CategoryUsages(ctx, accountID, from, to)
	if err != nil {
		return month{}, fmt.Errorf("reading category usage: %w", err)
	}

	if i := slices.IndexFunc(page.Usages, func(u store.CategoryUsage) bool { return u.ID == category }); i >= 0 {
		page.Filter = page.Usages[i]
	}

	pending, err := shell.Pending(ctx, p.db, accountID, now, last)
	if err != nil {
		return month{}, err
	}

	page.Previous = previous
	page.Daily = readDaily(session.From(ctx), first, page.Today, page.Usages, transactions)
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

		page.Pending += form.Signed(p.Recurrence.Kind, p.Recurrence.Amount)
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

		d := at(prefs.FormatDate(t.OccurredAt, now.Location(), time.DateOnly))
		d.Transactions = append(d.Transactions, t)
		d.Total += form.Signed(t.Kind, t.Amount)
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

	page.trim()

	return page, nil
}

// trim keeps the newest Limit entries (pending occurrences and transactions) of the statement, setting More when it cut
// any; the "hoje" line goes too when it falls in the cut part.
func (m *month) trim() {
	left := m.Limit
	for i, d := range m.Days {
		n := len(d.Pending) + len(d.Transactions)
		if n <= left {
			left -= n
			continue
		}

		m.More = true
		m.Days = m.Days[:i+1]
		if left == 0 {
			m.Days = m.Days[:i]
		} else if len(d.Pending) >= left {
			m.Days[i].Pending, m.Days[i].Transactions = d.Pending[:left], nil
		} else {
			m.Days[i].Transactions = d.Transactions[:left-len(d.Pending)]
		}

		break
	}

	if m.TodayAt > len(m.Days) {
		m.TodayAt = -1
	}
}

// statementLimit is the ?limit= of the request: how many entries the statement shows, at least one page.
func statementLimit(r *http.Request) int {
	limit, _ := strconv.Atoi(r.FormValue("limit"))

	return max(limit, statementPage)
}

// shownMonth is the month in ?month=YYYY-MM, or the current one, in now's location.
func shownMonth(r *http.Request, now time.Time) time.Time {
	shown, err := time.ParseInLocation(monthLayout, r.FormValue("month"), now.Location())
	if err != nil {
		return now
	}

	return shown
}

// Home is the Month screen of ?month=YYYY-MM, the current month in the user's time zone by default, with the statement
// narrowed to ?category=<id>.
func (p Page) Home(w http.ResponseWriter, r *http.Request) {
	s, ok := p.shell(w, r, "nav.month")
	if !ok {
		return
	}

	page, err := p.readMonth(r.Context(), session.From(r.Context()).AccountID, s.Now, shownMonth(r, s.Now), r.FormValue("category"), statementLimit(r))
	if err != nil {
		session.InternalError(w, "reading month", err)
		return
	}

	page.Flash = s.Flash
	session.Render(w, r, http.StatusOK, monthPage(s, page))
}

// CreateInMonth commits change, a new transaction, and answers the month on screen with its row flashing. A rejection
// answers 409 with the message conflict reads, or 500 when conflict is nil or reads none.
func (p Page) CreateInMonth(w http.ResponseWriter, r *http.Request, change store.Change, conflict func() (string, error)) {
	reason, err := p.db.CommitChanges(r.Context(), session.From(r.Context()), change)
	message := ""
	if err == nil && reason != "" && conflict != nil {
		message, err = conflict()
	}

	if err == nil && reason != "" && message == "" {
		err = fmt.Errorf("rejected: %s", reason)
	}

	if err != nil {
		session.InternalError(w, "creating transaction", err)
		return
	}

	p.renderMonth(w, r, utils.IfElse(message == "", http.StatusOK, http.StatusConflict), change.EntityID, message)
}

// renderMonth answers the month on screen, which a write in place swaps, with the row flash flashing and message as
// its error, along with the ticker it changed.
func (p Page) renderMonth(w http.ResponseWriter, r *http.Request, status int, flash, message string) {
	sess, now := session.From(r.Context()), time.Now().In(session.Location(r))
	page, err := p.readMonth(r.Context(), sess.AccountID, now, shownMonth(r, now), r.FormValue("category"), statementLimit(r))
	if err != nil {
		session.InternalError(w, "reading month", err)
		return
	}

	balance, err := shell.ReadBalance(r.Context(), p.db, sess.AccountID, now)
	if err != nil {
		session.InternalError(w, "reading balance", err)
		return
	}

	page.Flash, page.Error, page.Focus = flash, message, r.URL.Path == "/launch"
	session.Render(w, r, status, monthSwap(shell.Shell{Email: sess.Email, Balance: balance}, page))
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
