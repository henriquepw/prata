// Package categorypage is the Categories screen: the division of the monthly income and the category sheet.
package categorypage

import (
	"errors"
	"github.com/henriquepw/prata/server/internal/web/components/budget"
	"github.com/henriquepw/prata/server/internal/web/components/console"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"net/http"
	"strings"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/henriquepw/prata/server/internal/web/utils"
	"github.com/oklog/ulid/v2"
)

// Page is the categories' handlers: the Categories screen, its sheet and the division.
type Page struct {
	db    *store.Store
	shell shell.Func
}

// New returns the categories page over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell shell.Func) Page {
	return Page{db: db, shell: shell}
}

// NewSheet is the sheet of a new category.
func (p Page) NewSheet(w http.ResponseWriter, r *http.Request) {
	session.Render(w, r, http.StatusOK, categorySheet(categoryForm{Color: "GRAY"}))
}

// Categories is the Categories screen.
func (p Page) Categories(w http.ResponseWriter, r *http.Request) {
	categories, err := p.db.Categories(r.Context(), session.From(r.Context()).AccountID)
	if err != nil {
		session.InternalError(w, "listing categories", err)
		return
	}

	s, ok := p.shell(w, r, "nav.categories")
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, categoriesPage(s, budget.Stored(r, categories)))
}

// Edit is the sheet of a category.
func (p Page) Edit(w http.ResponseWriter, r *http.Request) {
	category, ok := p.category(w, r)
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, categorySheet(storedCategoryForm(category)))
}

// Save creates the category, or updates the one in the path, from the sheet's form.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
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

	if _, ok := prefs.Find(console.CategoryColors, form.Color); !ok {
		form.Errors["color"] = i18n.T(r.Context(), "categories.error.color")
	}

	change := store.Change{Entity: "CATEGORY", EntityID: form.ID, Kind: store.KindUpdate,
		Fields: map[string]any{"name": form.Name, "color": form.Color, "is_daily": int64(utils.IfElse(form.IsDaily, 1, 0))}}

	// The limits belong to the division (POST /budget): a new category enters with 0 and an edit leaves it.
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
		change.Fields["monthly_limit"] = int64(0)
	} else if stored, ok := p.category(w, r); ok {
		form.IsDefault = stored.IsDefault
	} else {
		return
	}

	// Marking another category as the default takes the mark from the current one (store.CommitChanges).
	if form.MarkDefault && !form.IsDefault {
		change.Fields["is_default"] = int64(1)
	}

	session.Commit(p.db, w, r, form.Errors, categorySheet(form), change)
}

// Delete removes the category in the path.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	category, ok := p.category(w, r)
	if !ok {
		return
	}

	form := storedCategoryForm(category)
	session.Commit(p.db, w, r, form.Errors, categorySheet(form), store.Change{Entity: "CATEGORY", EntityID: category.ID, Kind: store.KindDelete})
}

// storedCategoryForm fills the sheet with the stored category.
func storedCategoryForm(category store.Category) categoryForm {
	return categoryForm{ID: category.ID, Name: category.Name, Color: category.Color, IsDaily: category.IsDaily, IsDefault: category.IsDefault,
		Errors: map[string]string{}}
}

// category reads the live category in the path, answering 404 when the account has none.
func (p Page) category(w http.ResponseWriter, r *http.Request) (store.Category, bool) {
	category, err := p.db.Category(r.Context(), session.From(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Category{}, false
	}

	if err != nil {
		session.InternalError(w, "reading category", err)
		return store.Category{}, false
	}

	return category, true
}

// SaveBudget stores the monthly income and the division, or answers 422 with the screen's form, from what was typed,
// and stores nothing when a value is not an amount, the monthly income is not above zero or the division does not
// close it.
func (p Page) SaveBudget(w http.ResponseWriter, r *http.Request) {
	categories, ok := form.LiveCategories(w, r, p.db)
	if !ok {
		return
	}

	form, division := budget.Parse(r, categories)
	if len(form.Errors) == 0 {
		err := p.db.SaveBudget(r.Context(), session.From(r.Context()), division)
		if err != nil && !errors.Is(err, store.ErrDivisionNotClosed) {
			session.InternalError(w, "saving budget", err)
			return
		}

		if err == nil {
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(http.StatusNoContent)

			return
		}

		form.Errors["form"] = i18n.T(r.Context(), "budget.error.open")
	}

	session.Render(w, r, http.StatusUnprocessableEntity, budget.Screen(form, ""))
}
