package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/i18n"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
	"github.com/oklog/ulid/v2"
)

func (h handler) tags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.db.Tags(r.Context(), shared.SessionFrom(r.Context()).AccountID)
	if err != nil {
		shared.InternalError(w, "listing tags", err)
		return
	}

	s, ok := h.shell(w, r, "nav.tags")
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, tagsPage(s, tags))
}

func (h handler) editTag(w http.ResponseWriter, r *http.Request) {
	tag, ok := h.tag(w, r)
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, tagSheet(storedTagForm(tag, shared.Location(r))))
}

// saveTag creates the tag, or updates the one in the path, from the sheet's form. Dates are local midnights
// in the user's time zone, stored in UTC.
func (h handler) saveTag(w http.ResponseWriter, r *http.Request) {
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
	} else if _, ok := h.tag(w, r); !ok {
		return
	}

	h.commit(w, r, form.Errors, tagSheet(form), change)
}

func (h handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	tag, ok := h.tag(w, r)
	if !ok {
		return
	}

	form := storedTagForm(tag, shared.Location(r))
	h.commit(w, r, form.Errors, tagSheet(form), store.Change{Entity: "TAG", EntityID: tag.ID, Kind: store.KindDelete})
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
func (h handler) tag(w http.ResponseWriter, r *http.Request) (store.Tag, bool) {
	tag, err := h.db.Tag(r.Context(), shared.SessionFrom(r.Context()).AccountID, r.PathValue("id"))
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
