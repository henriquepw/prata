// Package movementpage is the Movimentos screen: the import of a file and the review of its pending movements.
package movementpage

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/internal/web/utils"
)

// Page is the movements' handlers.
type Page struct {
	db    *store.Store
	shell shell.Func
}

// New returns the movements page over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell shell.Func) Page {
	return Page{db: db, shell: shell}
}

// maxImport is the size limit of an imported file.
const maxImport = 1 << 20

// movementsView is the review screen: the pending movements and, after an import, its summary or the reason it was
// refused.
type movementsView struct {
	Categories  []store.Category
	Movements   []store.Movement
	Occurrences []shell.Occurrence
	Summary     string
	Error       string
}

// currencyView is the dialog that asks whether to import a file whose Moeda differs from the Conta's.
type currencyView struct {
	FileCurrency    string
	AccountCurrency string
}

// Movements is the review screen.
func (p Page) Movements(w http.ResponseWriter, r *http.Request) {
	p.renderMovements(w, r, http.StatusOK, movementsView{})
}

// Import reads the uploaded file into pending movements of the session's account, all or none, and answers
// the screen with the summary, or 422 with the reason and nothing stored.
func (p Page) Import(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImport+1<<16)

	var view movementsView
	statements, err := readImportFile(r)
	if err != nil {
		view.Error = i18n.T(r.Context(), "movements.error.file")
	} else {
		if r.PostFormValue("confirm_currency") == "" && askCurrency(w, r, statements) {
			return
		}

		sess := session.From(r.Context())
		added, existing, err := p.db.ImportMovements(r.Context(), sess.UserID, sess.AccountID, statements)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}

		if err != nil {
			session.InternalError(w, "importing movements", err)
			return
		}

		view.Summary = i18n.T(r.Context(), "movements.summary", added, existing)
	}

	status := http.StatusOK
	if view.Error != "" {
		status = http.StatusUnprocessableEntity
	}

	p.renderMovements(w, r, status, view)
}

// askCurrency answers, and reports it did, with the confirmation dialog swapped into #sheet when a statement
// declares a Moeda other than the session account's.
func askCurrency(w http.ResponseWriter, r *http.Request, statements []importfile.Statement) bool {
	currency := session.From(r.Context()).Currency
	for _, statement := range statements {
		if statement.Currency != "" && statement.Currency != currency {
			w.Header().Set("HX-Retarget", "#sheet")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("HX-Reselect", "#currency-dialog")
			session.Render(w, r, http.StatusOK, currencyDialog(currencyView{FileCurrency: statement.Currency, AccountCurrency: currency}))

			return true
		}
	}

	return false
}

// readImportFile reads the file of the multipart form, up to 1 MB, in any format it recognizes.
func readImportFile(r *http.Request) ([]importfile.Statement, error) {
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxImport+1))
	if err != nil {
		return nil, err
	}

	if len(content) > maxImport {
		return nil, importfile.ErrUnrecognized
	}

	return importfile.Parse(content)
}

func (p Page) renderMovements(w http.ResponseWriter, r *http.Request, status int, view movementsView) {
	sess := session.From(r.Context())

	var err error
	if view.Categories, err = p.db.Categories(r.Context(), sess.AccountID); err != nil {
		session.InternalError(w, "listing categories", err)
		return
	}

	if view.Movements, err = p.db.PendingMovements(r.Context(), sess); err != nil {
		session.InternalError(w, "listing movements", err)
		return
	}

	if view.Occurrences, err = p.pendingThisMonth(r); err != nil {
		session.InternalError(w, "listing pending occurrences", err)
		return
	}

	s, ok := p.shell(w, r, "nav.movements")
	if !ok {
		return
	}

	session.Render(w, r, status, movementsPage(s, view))
}

// movementDate writes a movement's date, as the file has it, in the Formato de data.
func movementDate(ctx context.Context, stored string) string {
	date, err := time.Parse(time.DateOnly, stored)
	if err != nil {
		return stored
	}

	return date.Format(prefs.DateLayout(ctx))
}

// Confirm makes the pending movement in the path a transaction with the description and category of the
// form, then answers the screen. A movement already reviewed is not made twice.
func (p Page) Confirm(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := p.formCategory(w, r)
	if !ok {
		return
	}

	description := strings.TrimSpace(r.PostFormValue("description"))
	reason, err := p.db.ConfirmMovement(r.Context(), session.From(r.Context()), r.PathValue("id"), description, categoryID, session.Location(r))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		session.InternalError(w, "confirming movement", err)
		return
	}

	if reason != "" {
		slog.Warn("movement confirm rejected", "reason", reason)
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}

	p.renderMovements(w, r, http.StatusOK, movementsView{})
}

// formCategory is the category_id of the form, empty or one of the account's live categories; any other answers 422.
func (p Page) formCategory(w http.ResponseWriter, r *http.Request) (string, bool) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return "", false
	}

	categoryID := r.PostFormValue("category_id")
	if categoryID != "" && !form.HasCategory(categories, categoryID) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return "", false
	}

	return categoryID, true
}

// Ignore drops the pending movement in the path from review for good, then answers the screen.
func (p Page) Ignore(w http.ResponseWriter, r *http.Request) {
	if err := p.db.IgnoreMovement(r.Context(), session.From(r.Context()), r.PathValue("id")); err != nil && !errors.Is(err, store.ErrNotFound) {
		session.InternalError(w, "ignoring movement", err)
		return
	}

	p.renderMovements(w, r, http.StatusOK, movementsView{})
}

// ConfirmAll makes every pending movement among the selected ids a transaction with the bank's description
// and the batch category, as one commit, then answers the screen.
func (p Page) ConfirmAll(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := p.formCategory(w, r)
	if !ok {
		return
	}

	reason, err := p.db.ConfirmMovements(r.Context(), session.From(r.Context()), r.PostForm["id"], categoryID, session.Location(r))
	if err != nil {
		session.InternalError(w, "confirming movements", err)
		return
	}

	if reason != "" {
		slog.Warn("movements confirm rejected", "reason", reason)
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}

	p.renderMovements(w, r, http.StatusOK, movementsView{})
}

// IgnoreAll drops every pending movement among the selected ids from review for good, then answers the screen.
func (p Page) IgnoreAll(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	if err := p.db.IgnoreMovements(r.Context(), session.From(r.Context()), r.PostForm["id"]); err != nil {
		session.InternalError(w, "ignoring movements", err)
		return
	}

	p.renderMovements(w, r, http.StatusOK, movementsView{})
}

// pendingThisMonth are the account's pending occurrences up to the end of the current month, the ones the ticker counts.
func (p Page) pendingThisMonth(r *http.Request) ([]shell.Occurrence, error) {
	now := time.Now().In(session.Location(r))
	month, _, _ := form.MonthWindow(now)

	return shell.Pending(r.Context(), p.db, session.From(r.Context()).AccountID, now, month.AddDate(0, 1, -1))
}

// occurrencesFor are the pending occurrences of the movement's kind, the ones of an equal amount first and then the
// closest in date to the movement's.
func occurrencesFor(m store.Movement, pending []shell.Occurrence) []shell.Occurrence {
	day, _ := time.Parse(time.DateOnly, m.OccurredAt)
	distance := func(o shell.Occurrence) time.Duration {
		date, _ := time.Parse(time.DateOnly, o.Date)

		return max(date.Sub(day), day.Sub(date))
	}
	isEqual := func(o shell.Occurrence) bool { return o.Recurrence.Amount == m.Amount }

	matches := slices.DeleteFunc(slices.Clone(pending), func(o shell.Occurrence) bool { return o.Recurrence.Kind != m.Kind })
	slices.SortStableFunc(matches, func(a, b shell.Occurrence) int {
		return cmp.Or(
			cmp.Compare(utils.IfElse(isEqual(a), 0, 1), utils.IfElse(isEqual(b), 0, 1)),
			cmp.Compare(distance(a), distance(b)),
		)
	})

	return matches
}

// ConfirmAsOccurrence makes the pending movement in the path the confirmation of the occurrence of the form,
// "<recurrence id>:<date>", then answers the screen. Only a pending occurrence of the movement's kind is accepted
// (422 otherwise), and one confirmed meanwhile, by another movement or by hand, creates nothing (409).
func (p Page) ConfirmAsOccurrence(w http.ResponseWriter, r *http.Request) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	recurrenceID, date, _ := strings.Cut(r.PostFormValue("occurrence"), ":")
	pending, err := p.pendingThisMonth(r)
	if err != nil {
		session.InternalError(w, "listing pending occurrences", err)
		return
	}

	i := slices.IndexFunc(pending, func(o shell.Occurrence) bool { return o.Recurrence.ID == recurrenceID && o.Date == date })
	if i < 0 {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}

	categoryID := form.OrEmpty(pending[i].Recurrence.CategoryID)
	if !form.HasCategory(categories, categoryID) {
		categoryID = ""
	}

	reason, err := p.db.ConfirmMovementAsOccurrence(r.Context(), session.From(r.Context()), r.PathValue("id"), recurrenceID, date, "", categoryID, session.Location(r))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		session.InternalError(w, "confirming movement as occurrence", err)
		return
	}

	if reason != "" {
		http.Error(w, http.StatusText(http.StatusConflict), http.StatusConflict)
		return
	}

	p.renderMovements(w, r, http.StatusOK, movementsView{})
}
