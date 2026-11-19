package web

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

const rejectedMessage = "Não foi possível salvar. Recarregue a página e tente de novo."

func (h handler) categories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.db.Categories(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing categories", err)
		return
	}

	render(w, r, http.StatusOK, categoriesPage(newShell(r, "Categorias"), categories))
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
		ID:           r.PathValue("id"),
		Name:         strings.TrimSpace(r.PostFormValue("name")),
		Color:        r.PostFormValue("color"),
		MonthlyLimit: strings.TrimSpace(r.PostFormValue("monthly_limit")),
		Errors:       map[string]string{},
	}
	if form.Name == "" {
		form.Errors["name"] = "Informe um nome."
	}

	if _, ok := findColor(form.Color); !ok {
		form.Errors["color"] = "Escolha uma cor."
	}

	limit, ok := parseCents(form.MonthlyLimit)
	if !ok || limit < 0 {
		form.Errors["monthly_limit"] = "Informe um limite maior ou igual a zero."
	}

	change := store.Change{Entity: "CATEGORY", EntityID: form.ID, Kind: store.KindUpdate,
		Fields: map[string]any{"name": form.Name, "color": form.Color, "monthly_limit": limit}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else if _, ok := h.category(w, r); !ok {
		return
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
	return categoryForm{ID: category.ID, Name: category.Name, Color: category.Color, MonthlyLimit: formatCents(category.MonthlyLimit), Errors: map[string]string{}}
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
// and asks htmx to reload the page. The sheet must render from fieldErrors, so the rejection shows up in it.
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
		fieldErrors["form"] = rejectedMessage
		render(w, r, http.StatusUnprocessableEntity, sheet)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

func findColor(value string) (categoryColor, bool) {
	i := slices.IndexFunc(categoryColors, func(color categoryColor) bool { return color.Value == value })
	if i < 0 {
		return categoryColor{}, false
	}

	return categoryColors[i], true
}

func colorClass(value string) string {
	color, _ := findColor(value)

	return color.Class
}
