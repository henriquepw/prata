// Package tagpage is the Tags screen and the tag sheet.
package tagpage

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/oklog/ulid/v2"
)

// Page is the tags' handlers: the Tags screen and its sheet.
type Page struct {
	db    *store.Store
	shell func(w http.ResponseWriter, r *http.Request, title string) (shell.Shell, bool)
}

// New returns the tags page over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell func(w http.ResponseWriter, r *http.Request, title string) (shell.Shell, bool)) Page {
	return Page{db: db, shell: shell}
}

// Tags is the Tags screen.
func (p Page) Tags(w http.ResponseWriter, r *http.Request) {
	tags, err := p.db.Tags(r.Context(), shared.SessionFrom(r.Context()).AccountID)
	if err != nil {
		shared.InternalError(w, "listing tags", err)
		return
	}

	s, ok := p.shell(w, r, "nav.tags")
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, tagsPage(s, tags))
}

// NewSheet is the sheet of a new tag.
func (p Page) NewSheet(w http.ResponseWriter, r *http.Request) {
	shared.Render(w, r, http.StatusOK, tagSheet(tagForm{}))
}

// Edit is the sheet of a tag.
func (p Page) Edit(w http.ResponseWriter, r *http.Request) {
	tag, ok := p.tag(w, r)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, tagSheet(storedTagForm(tag, shared.Location(r))))
}

// Save creates the tag, or updates the one in the path, from the sheet's form. Dates are local midnights
// in the user's time zone, stored in UTC.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	form := tagForm{
		ID:      r.PathValue("id"),
		Name:    strings.TrimSpace(r.PostFormValue("name")),
		StartAt: r.PostFormValue("start_at"),
		EndAt:   r.PostFormValue("end_at"),
		Errors:  map[string]string{},
	}
	if form.Name == "" {
		form.Errors["name"] = i18n.T(r.Context(), "form.error.name")
	}

	loc := shared.Location(r)
	startAt, ok := shared.ParseDate(form.StartAt, loc)
	if !ok {
		form.Errors["start_at"] = i18n.T(r.Context(), "form.error.start")
	}

	var endAt any
	if form.EndAt != "" {
		end, ok := shared.ParseDate(form.EndAt, loc)
		if !ok || (startAt != "" && end < startAt) {
			form.Errors["end_at"] = i18n.T(r.Context(), "form.error.end")
		}

		endAt = end
	}

	change := store.Change{Entity: "TAG", EntityID: form.ID, Kind: store.KindUpdate,
		Fields: map[string]any{"name": form.Name, "start_at": startAt, "end_at": endAt}}
	if form.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else if _, ok := p.tag(w, r); !ok {
		return
	}

	shared.Commit(p.db, w, r, form.Errors, tagSheet(form), change)
}

// Delete tombstones the tag in the path.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	tag, ok := p.tag(w, r)
	if !ok {
		return
	}

	form := storedTagForm(tag, shared.Location(r))
	shared.Commit(p.db, w, r, form.Errors, tagSheet(form), store.Change{Entity: "TAG", EntityID: tag.ID, Kind: store.KindDelete})
}

// storedTagForm fills the sheet with the stored tag, its dates in the user's time zone.
func storedTagForm(tag store.Tag, loc *time.Location) tagForm {
	form := tagForm{ID: tag.ID, Name: tag.Name, StartAt: shared.FormatDate(tag.StartAt, loc, time.DateOnly), Errors: map[string]string{}}
	if tag.EndAt != nil {
		form.EndAt = shared.FormatDate(*tag.EndAt, loc, time.DateOnly)
	}

	return form
}

// tag reads the live tag in the path, answering 404 when the account has none.
func (p Page) tag(w http.ResponseWriter, r *http.Request) (store.Tag, bool) {
	tag, err := p.db.Tag(r.Context(), shared.SessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Tag{}, false
	}

	if err != nil {
		shared.InternalError(w, "reading tag", err)
		return store.Tag{}, false
	}

	return tag, true
}
