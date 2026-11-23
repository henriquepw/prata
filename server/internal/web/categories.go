package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/utils"
	"github.com/oklog/ulid/v2"
)

func (h handler) categories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.db.Categories(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing categories", err)
		return
	}

	s, ok := h.shell(w, r, "nav.categories")
	if !ok {
		return
	}

	render(w, r, http.StatusOK, categoriesPage(s, storedBudgetForm(r, categories)))
}

func (h handler) editCategory(w http.ResponseWriter, r *http.Request) {
	category, ok := h.category(w, r)
	if !ok {
		return
	}

	render(w, r, http.StatusOK, categorySheet(storedCategoryForm(category)))
}

// saveCategory creates the category, or updates the one in the path, from the sheet's form.
func (h handler) saveCategory(w http.ResponseWriter, r *http.Request) {
	form := categoryForm{
		ID:          r.PathValue("id"),
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		Color:       r.PostFormValue("color"),
		IsDaily:     r.PostFormValue("is_daily") == "1",
		MarkDefault: r.PostFormValue("is_default") == "1",
		Errors:      map[string]string{},
	}
	if form.Name == "" {
		form.Errors["name"] = i18n.T(r.Context(), "form.error.name")
	}

	if _, ok := find(categoryColors, form.Color); !ok {
		form.Errors["color"] = i18n.T(r.Context(), "categories.error.color")
	}

	change := store.Change{Entity: "CATEGORY", EntityID: form.ID, Kind: store.KindUpdate,
		Fields: map[string]any{"name": form.Name, "color": form.Color, "is_daily": int64(utils.IfElse(form.IsDaily, 1, 0))}}

	// The limits belong to the division (POST /budget): a new category enters with 0 and an edit leaves it.
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
		change.Fields["monthly_limit"] = int64(0)
	} else if stored, ok := h.category(w, r); ok {
		form.IsDefault = stored.IsDefault
	} else {
		return
	}

	// Marking another category as the default takes the mark from the current one (store.CommitChanges).
	if form.MarkDefault && !form.IsDefault {
		change.Fields["is_default"] = int64(1)
	}

	h.commit(w, r, form.Errors, categorySheet(form), change)
}

func (h handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	category, ok := h.category(w, r)
	if !ok {
		return
	}

	form := storedCategoryForm(category)
	h.commit(w, r, form.Errors, categorySheet(form), store.Change{Entity: "CATEGORY", EntityID: category.ID, Kind: store.KindDelete})
}

// storedCategoryForm fills the sheet with the stored category.
func storedCategoryForm(category store.Category) categoryForm {
	return categoryForm{ID: category.ID, Name: category.Name, Color: category.Color, IsDaily: category.IsDaily, IsDefault: category.IsDefault,
		Errors: map[string]string{}}
}

// category reads the live category in the path, answering 404 when the account has none.
func (h handler) category(w http.ResponseWriter, r *http.Request) (store.Category, bool) {
	category, err := h.db.Category(r.Context(), sessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Category{}, false
	}

	if err != nil {
		internalError(w, "reading category", err)
		return store.Category{}, false
	}

	return category, true
}

// commit is the end of every sheet submit: with field errors, or when CommitChanges rejects the changes,
// it answers 422 with the sheet (fieldErrors["form"] set on rejection) and writes nothing; otherwise it commits
// and asks htmx to reload the page, flashing the first change's row unless it was deleted; only its list reads the
// flash, so a sheet opened from another screen leaves it for when the list opens. The sheet must render
// from fieldErrors, so the rejection shows up in it.
func (h handler) commit(w http.ResponseWriter, r *http.Request, fieldErrors map[string]string, sheet templ.Component, changes ...store.Change) {
	if len(fieldErrors) > 0 {
		render(w, r, http.StatusUnprocessableEntity, sheet)
		return
	}

	reason, err := h.db.CommitChanges(r.Context(), sessionFrom(r.Context()), changes...)
	if err != nil {
		internalError(w, "committing web changes", err)
		return
	}

	if reason != "" {
		slog.Warn("web changes rejected", "reason", reason)
		fieldErrors["form"] = i18n.T(r.Context(), "form.error.rejected")
		render(w, r, http.StatusUnprocessableEntity, sheet)
		return
	}

	if changes[0].Kind != store.KindDelete {
		http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: changes[0].EntityID, Path: returnPath(changes[0].Entity, r), MaxAge: 60, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// returnPath is the page a sheet submit returns to, which shows what it wrote: the Mês for a transaction, and otherwise
// its own list, as "/tags" for POST /tags/{id}.
func returnPath(entity string, r *http.Request) string {
	if entity == "TRANSACTION" {
		return "/"
	}

	return "/" + strings.Split(r.URL.Path, "/")[1]
}

func colorClass(value string) string {
	color, _ := find(categoryColors, value)

	return color.Class
}
