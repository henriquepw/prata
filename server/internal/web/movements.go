package web

import (
	"context"
	"errors"
	"io"
	"net/http"
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
	Accounts  []store.AccountRef
	Movements []store.Movement
	Summary   string
	Error     string
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
