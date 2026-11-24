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
	"github.com/henriquepw/prata/server/internal/web/form"
	"github.com/henriquepw/prata/server/internal/web/prefs"
	"github.com/henriquepw/prata/server/internal/web/session"
	"github.com/oklog/ulid/v2"
)

// Page is the tags' handlers: the Tags screen and its sheet.
type Page struct {
	db    *store.Store
	shell shell.Func
}

// New returns the tags page over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell shell.Func) Page {
	return Page{db: db, shell: shell}
}

// Tags is the Tags screen.
func (p Page) Tags(w http.ResponseWriter, r *http.Request) {
	tags, err := p.db.Tags(r.Context(), session.From(r.Context()).AccountID)
	if err != nil {
		session.InternalError(w, "listing tags", err)
		return
	}

	s, ok := p.shell(w, r, "nav.tags")
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, tagsPage(s, tags))
}

// NewSheet is the sheet of a new tag.
func (p Page) NewSheet(w http.ResponseWriter, r *http.Request) {
	session.Render(w, r, http.StatusOK, tagSheet(tagForm{}))
}

// Edit is the sheet of a tag.
func (p Page) Edit(w http.ResponseWriter, r *http.Request) {
	tag, ok := p.tag(w, r)
	if !ok {
		return
	}

	session.Render(w, r, http.StatusOK, tagSheet(storedTagForm(tag, session.Location(r))))
}

// Save creates the tag, or updates the one in the path, from the sheet's form. Dates are local midnights
// in the user's time zone, stored in UTC.
func (p Page) Save(w http.ResponseWriter, r *http.Request) {
	fm := tagForm{
		ID:      r.PathValue("id"),
		Name:    strings.TrimSpace(r.PostFormValue("name")),
		StartAt: r.PostFormValue("start_at"),
		EndAt:   r.PostFormValue("end_at"),
		Errors:  map[string]string{},
	}
	if fm.Name == "" {
		fm.Errors["name"] = i18n.T(r.Context(), "form.error.name")
	}

	loc := session.Location(r)
	startAt, ok := form.ParseDate(fm.StartAt, loc)
	if !ok {
		fm.Errors["start_at"] = i18n.T(r.Context(), "form.error.start")
	}

	var endAt any
	if fm.EndAt != "" {
		end, ok := form.ParseDate(fm.EndAt, loc)
		if !ok || (startAt != "" && end < startAt) {
			fm.Errors["end_at"] = i18n.T(r.Context(), "form.error.end")
		}

		endAt = end
	}

	change := store.Change{Entity: "TAG", EntityID: fm.ID, Kind: store.KindUpdate,
		Fields: map[string]any{"name": fm.Name, "start_at": startAt, "end_at": endAt}}
	if fm.ID == "" {
		change.EntityID, change.Kind = ulid.Make().String(), store.KindCreate
	} else if _, ok := p.tag(w, r); !ok {
		return
	}

	session.Commit(p.db, w, r, fm.Errors, tagSheet(fm), change)
}

// Delete tombstones the tag in the path.
func (p Page) Delete(w http.ResponseWriter, r *http.Request) {
	tag, ok := p.tag(w, r)
	if !ok {
		return
	}

	fm := storedTagForm(tag, session.Location(r))
	session.Commit(p.db, w, r, fm.Errors, tagSheet(fm), store.Change{Entity: "TAG", EntityID: tag.ID, Kind: store.KindDelete})
}

// storedTagForm fills the sheet with the stored tag, its dates in the user's time zone.
func storedTagForm(tag store.Tag, loc *time.Location) tagForm {
	fm := tagForm{ID: tag.ID, Name: tag.Name, StartAt: prefs.FormatDate(tag.StartAt, loc, time.DateOnly), Errors: map[string]string{}}
	if tag.EndAt != nil {
		fm.EndAt = prefs.FormatDate(*tag.EndAt, loc, time.DateOnly)
	}

	return fm
}

// tag reads the live tag in the path, answering 404 when the account has none.
func (p Page) tag(w http.ResponseWriter, r *http.Request) (store.Tag, bool) {
	tag, err := p.db.Tag(r.Context(), session.From(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Tag{}, false
	}

	if err != nil {
		session.InternalError(w, "reading tag", err)
		return store.Tag{}, false
	}

	return tag, true
}
