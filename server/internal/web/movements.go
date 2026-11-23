package web

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
	"github.com/henriquepw/prata/server/internal/web/utils"
)

// maxImport is the size limit of an imported file.
const maxImport = 1 << 20

// movementsView is the review screen: the pending movements and, after an import, its summary or the reason it was
// refused.
type movementsView struct {
	Categories  []store.Category
	Movements   []store.Movement
	Occurrences []occurrence
	Summary     string
	Error       string
}

// currencyView is the dialog that asks whether to import a file whose Moeda differs from the Conta's.
type currencyView struct {
	FileCurrency    string
	AccountCurrency string
}

func (h handler) movements(w http.ResponseWriter, r *http.Request) {
	h.renderMovements(w, r, http.StatusOK, movementsView{})
}

// importMovements reads the uploaded file into pending movements of the session's account, all or none, and answers
// the screen with the summary, or 422 with the reason and nothing stored.
func (h handler) importMovements(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImport+1<<16)

	var view movementsView
	statements, err := readImportFile(r)
	if err != nil {
		view.Error = i18n.T(r.Context(), "movements.error.file")
	} else {
		if r.PostFormValue("confirm_currency") == "" && askCurrency(w, r, statements) {
			return
		}

		session := sessionFrom(r.Context())
		added, existing, err := h.db.ImportMovements(r.Context(), session.UserID, session.AccountID, statements)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}

		if err != nil {
			internalError(w, "importing movements", err)
			return
		}

		view.Summary = i18n.T(r.Context(), "movements.summary", added, existing)
	}

	status := http.StatusOK
	if view.Error != "" {
		status = http.StatusUnprocessableEntity
	}

	h.renderMovements(w, r, status, view)
}

// askCurrency answers, and reports it did, with the confirmation dialog swapped into #sheet when a statement
// declares a Moeda other than the session account's.
func askCurrency(w http.ResponseWriter, r *http.Request, statements []importfile.Statement) bool {
	currency := sessionFrom(r.Context()).Currency
	for _, statement := range statements {
		if statement.Currency != "" && statement.Currency != currency {
			w.Header().Set("HX-Retarget", "#sheet")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("HX-Reselect", "#currency-dialog")
			render(w, r, http.StatusOK, currencyDialog(currencyView{FileCurrency: statement.Currency, AccountCurrency: currency}))

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

func (h handler) renderMovements(w http.ResponseWriter, r *http.Request, status int, view movementsView) {
	session := sessionFrom(r.Context())

	var err error
	if view.Categories, err = h.db.Categories(r.Context(), session.AccountID); err != nil {
		internalError(w, "listing categories", err)
		return
	}

	if view.Movements, err = h.db.PendingMovements(r.Context(), session); err != nil {
		internalError(w, "listing movements", err)
		return
	}

	if view.Occurrences, err = h.pendingThisMonth(r); err != nil {
		internalError(w, "listing pending occurrences", err)
		return
	}

	s, ok := h.shell(w, r, "nav.movements")
	if !ok {
		return
	}

	render(w, r, status, movementsPage(s, view))
}

// movementDate writes a movement's date, as the file has it, in the Formato de data.
func movementDate(ctx context.Context, stored string) string {
	date, err := time.Parse(time.DateOnly, stored)
	if err != nil {
		return stored
	}

	return date.Format(dateLayout(ctx))
}

// confirmMovement makes the pending movement in the path a transaction with the description and category of the
// form, then answers the screen. A movement already reviewed is not made twice.
func (h handler) confirmMovement(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := h.formCategory(w, r)
	if !ok {
		return
	}

	description := strings.TrimSpace(r.PostFormValue("description"))
	reason, err := h.db.ConfirmMovement(r.Context(), sessionFrom(r.Context()), r.PathValue("id"), description, categoryID, location(r))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, "confirming movement", err)
		return
	}

	if reason != "" {
		slog.Warn("movement confirm rejected", "reason", reason)
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}

	h.renderMovements(w, r, http.StatusOK, movementsView{})
}

// formCategory is the category_id of the form, empty or one of the account's live categories; any other answers 422.
func (h handler) formCategory(w http.ResponseWriter, r *http.Request) (string, bool) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return "", false
	}

	categoryID := r.PostFormValue("category_id")
	if categoryID != "" && !hasCategory(categories, categoryID) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return "", false
	}

	return categoryID, true
}

// ignoreMovement drops the pending movement in the path from review for good, then answers the screen.
func (h handler) ignoreMovement(w http.ResponseWriter, r *http.Request) {
	if err := h.db.IgnoreMovement(r.Context(), sessionFrom(r.Context()), r.PathValue("id")); err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, "ignoring movement", err)
		return
	}

	h.renderMovements(w, r, http.StatusOK, movementsView{})
}

// confirmMovements makes every pending movement among the selected ids a transaction with the bank's description
// and the batch category, as one commit, then answers the screen.
func (h handler) confirmMovements(w http.ResponseWriter, r *http.Request) {
	categoryID, ok := h.formCategory(w, r)
	if !ok {
		return
	}

	reason, err := h.db.ConfirmMovements(r.Context(), sessionFrom(r.Context()), r.PostForm["id"], categoryID, location(r))
	if err != nil {
		internalError(w, "confirming movements", err)
		return
	}

	if reason != "" {
		slog.Warn("movements confirm rejected", "reason", reason)
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}

	h.renderMovements(w, r, http.StatusOK, movementsView{})
}

// ignoreMovements drops every pending movement among the selected ids from review for good, then answers the screen.
func (h handler) ignoreMovements(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	if err := h.db.IgnoreMovements(r.Context(), sessionFrom(r.Context()), r.PostForm["id"]); err != nil {
		internalError(w, "ignoring movements", err)
		return
	}

	h.renderMovements(w, r, http.StatusOK, movementsView{})
}

// pendingThisMonth are the account's pending occurrences up to the end of the current month, the ones the ticker counts.
func (h handler) pendingThisMonth(r *http.Request) ([]occurrence, error) {
	now := time.Now().In(location(r))
	month, _, _ := monthWindow(now)

	return h.pending(r.Context(), sessionFrom(r.Context()).AccountID, now, month.AddDate(0, 1, -1))
}

// occurrencesFor are the pending occurrences of the movement's kind, the ones of an equal amount first and then the
// closest in date to the movement's.
func occurrencesFor(m store.Movement, pending []occurrence) []occurrence {
	day, _ := time.Parse(time.DateOnly, m.OccurredAt)
	distance := func(o occurrence) time.Duration {
		date, _ := time.Parse(time.DateOnly, o.Date)

		return max(date.Sub(day), day.Sub(date))
	}
	isEqual := func(o occurrence) bool { return o.Recurrence.Amount == m.Amount }

	matches := slices.DeleteFunc(slices.Clone(pending), func(o occurrence) bool { return o.Recurrence.Kind != m.Kind })
	slices.SortStableFunc(matches, func(a, b occurrence) int {
		return cmp.Or(
			cmp.Compare(utils.IfElse(isEqual(a), 0, 1), utils.IfElse(isEqual(b), 0, 1)),
			cmp.Compare(distance(a), distance(b)),
		)
	})

	return matches
}

// confirmMovementAsOccurrence makes the pending movement in the path the confirmation of the occurrence of the form,
// "<recurrence id>:<date>", then answers the screen. Only a pending occurrence of the movement's kind is accepted
// (422 otherwise), and one confirmed meanwhile, by another movement or by hand, creates nothing (409).
func (h handler) confirmMovementAsOccurrence(w http.ResponseWriter, r *http.Request) {
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	recurrenceID, date, _ := strings.Cut(r.PostFormValue("occurrence"), ":")
	pending, err := h.pendingThisMonth(r)
	if err != nil {
		internalError(w, "listing pending occurrences", err)
		return
	}

	i := slices.IndexFunc(pending, func(o occurrence) bool { return o.Recurrence.ID == recurrenceID && o.Date == date })
	if i < 0 {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
		return
	}

	categoryID := orEmpty(pending[i].Recurrence.CategoryID)
	if !hasCategory(categories, categoryID) {
		categoryID = ""
	}

	reason, err := h.db.ConfirmMovementAsOccurrence(r.Context(), sessionFrom(r.Context()), r.PathValue("id"), recurrenceID, date, "", categoryID, location(r))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, "confirming movement as occurrence", err)
		return
	}

	if reason != "" {
		http.Error(w, http.StatusText(http.StatusConflict), http.StatusConflict)
		return
	}

	h.renderMovements(w, r, http.StatusOK, movementsView{})
}
