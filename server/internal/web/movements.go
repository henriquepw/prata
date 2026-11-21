package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/importfile"
	"github.com/henriquepw/prata/server/internal/store"
)

// maxImport is the size limit of an imported file.
const maxImport = 1 << 20

// movementsView is the review screen: the accounts to import into, the pending movements and, after an import, its
// summary or the reason it was refused.
type movementsView struct {
	Accounts   []store.AccountRef
	Categories []store.Category
	Movements  []store.Movement
	Summary    string
	Error      string
}

// currencyView is the dialog that asks whether to import a file whose Moeda differs from the Conta's.
type currencyView struct {
	File    string
	Account string
}

func (h handler) movements(w http.ResponseWriter, r *http.Request) {
	h.renderMovements(w, r, http.StatusOK, movementsView{})
}

// importMovements reads the uploaded file into pending movements of the chosen account, all or none, and answers
// the screen with the summary, or 422 with the reason and nothing stored.
func (h handler) importMovements(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImport+1<<16)

	var view movementsView
	statements, err := readUpload(r)
	switch {
	case err != nil:
		view.Error = i18n.T(r.Context(), "movements.error.file")
	case r.PostFormValue("confirm_currency") == "" && h.askCurrency(w, r, statements):
		return
	default:
		session := sessionFrom(r.Context())
		added, existing, err := h.db.ImportMovements(r.Context(), session.UserID, r.PostFormValue("account_id"), statements)
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

// askCurrency answers with the confirmation dialog, swapped into #sheet, when a statement declares a Moeda other
// than the chosen account's, and reports whether it did.
func (h handler) askCurrency(w http.ResponseWriter, r *http.Request, statements []importfile.Statement) bool {
	accounts, err := h.db.UserAccounts(r.Context(), sessionFrom(r.Context()).UserID)
	if err != nil {
		return false
	}

	for _, account := range accounts {
		if account.ID != r.PostFormValue("account_id") {
			continue
		}

		for _, statement := range statements {
			if statement.Currency != "" && statement.Currency != account.Currency {
				w.Header().Set("HX-Retarget", "#sheet")
				w.Header().Set("HX-Reswap", "innerHTML")
				w.Header().Set("HX-Reselect", "#currency-dialog")
				render(w, r, http.StatusOK, currencyDialog(currencyView{File: statement.Currency, Account: account.Currency}))

				return true
			}
		}
	}

	return false
}

// readUpload reads the file of the multipart form, up to 1 MB, in any format it recognizes.
func readUpload(r *http.Request) ([]importfile.Statement, error) {
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
	userID := sessionFrom(r.Context()).UserID

	var err error
	if view.Accounts, err = h.db.UserAccounts(r.Context(), userID); err != nil {
		internalError(w, "listing accounts", err)
		return
	}

	if view.Categories, err = h.db.Categories(r.Context(), sessionFrom(r.Context()).AccountID); err != nil {
		internalError(w, "listing categories", err)
		return
	}

	if view.Movements, err = h.db.PendingMovements(r.Context(), userID); err != nil {
		internalError(w, "listing movements", err)
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
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	categoryID := r.PostFormValue("category_id")
	if categoryID != "" && !hasCategory(categories, categoryID) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
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
	categories, ok := h.liveCategories(w, r)
	if !ok {
		return
	}

	categoryID := r.PostFormValue("category_id")
	if categoryID != "" && !hasCategory(categories, categoryID) {
		http.Error(w, http.StatusText(http.StatusUnprocessableEntity), http.StatusUnprocessableEntity)
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
