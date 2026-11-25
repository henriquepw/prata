package session

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/l10n"
	"github.com/henriquepw/prata/server/internal/store"
)

// Commit is the end of every sheet submit: with field errors, or when CommitChanges rejects the changes,
// it answers 422 with the sheet (fieldErrors["form"] set on rejection) and writes nothing; otherwise it commits
// and asks htmx to reload the page, flashing the first change's row unless it was deleted; only its list reads the
// flash, so a sheet opened from another screen leaves it for when the list opens. The sheet must render
// from fieldErrors, so the rejection shows up in it.
func Commit(db *store.Store, w http.ResponseWriter, r *http.Request, fieldErrors map[string]string, sheet templ.Component, changes ...store.Change) {
	if len(fieldErrors) > 0 {
		Render(w, r, http.StatusUnprocessableEntity, sheet)
		return
	}

	reason, err := db.CommitChanges(r.Context(), From(r.Context()), changes...)
	if err != nil {
		InternalError(w, "committing web changes", err)
		return
	}

	if reason != "" {
		slog.Warn("web changes rejected", "reason", reason)
		fieldErrors["form"] = l10n.T(r.Context(), "form.error.rejected")
		Render(w, r, http.StatusUnprocessableEntity, sheet)
		return
	}

	if changes[0].Kind != store.KindDelete {
		http.SetCookie(w, &http.Cookie{Name: FlashCookie, Value: changes[0].EntityID.String(), Path: returnPath(changes[0].Entity, r), MaxAge: 60, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// returnPath is the page a sheet submit returns to, which shows what it wrote: the Month for a transaction, and otherwise
// its own list, as "/tags" for POST /tags/{id}.
func returnPath(entity string, r *http.Request) string {
	if entity == "TRANSACTION" {
		return "/"
	}

	return "/" + strings.Split(r.URL.Path, "/")[1]
}
