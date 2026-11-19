package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/oklog/ulid/v2"
)

const dateLayout = "2006-01-02"

func (h handler) tags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.db.Tags(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing tags", err)
		return
	}

	render(w, r, http.StatusOK, tagsPage(newShell(r, "Tags"), tags, location(r)))
}

func (h handler) editTag(w http.ResponseWriter, r *http.Request) {
	tag, ok := h.tag(w, r)
	if !ok {
		return
	}

	render(w, r, http.StatusOK, tagSheet(storedTagForm(tag, location(r))))
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
		form.Errors["name"] = "Informe um nome."
	}

	loc := location(r)
	startAt, ok := parseDate(form.StartAt, loc)
	if !ok {
		form.Errors["start_at"] = "Informe o início."
	}

	var endAt any
	if form.EndAt != "" {
		end, ok := parseDate(form.EndAt, loc)
		if !ok || (startAt != "" && end < startAt) {
			form.Errors["end_at"] = "Informe um fim igual ou depois do início."
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

	form := storedTagForm(tag, location(r))
	h.commit(w, r, form.Errors, tagSheet(form), store.Change{Entity: "TAG", EntityID: tag.ID, Kind: store.KindDelete})
}

// storedTagForm fills the sheet with the stored tag, its dates in the user's time zone.
func storedTagForm(tag store.Tag, loc *time.Location) tagForm {
	form := tagForm{ID: tag.ID, Name: tag.Name, StartAt: formatDate(tag.StartAt, loc, dateLayout), Errors: map[string]string{}}
	if tag.EndAt != nil {
		form.EndAt = formatDate(*tag.EndAt, loc, dateLayout)
	}

	return form
}

// tag reads the live tag in the path, answering 404 when the account has none.
func (h handler) tag(w http.ResponseWriter, r *http.Request) (store.Tag, bool) {
	tag, err := h.db.Tag(r.Context(), sessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return store.Tag{}, false
	}

	if err != nil {
		internalError(w, "reading tag", err)
		return store.Tag{}, false
	}

	return tag, true
}

// parseDate reads a date input's YYYY-MM-DD as local midnight and returns it as RFC 3339 in UTC.
func parseDate(s string, loc *time.Location) (string, bool) {
	t, err := time.ParseInLocation(dateLayout, s, loc)
	if err != nil {
		return "", false
	}

	return t.UTC().Format(time.RFC3339), true
}

// formatDate writes a stored RFC 3339 time as a date in loc, or the raw value when it does not parse.
func formatDate(stored string, loc *time.Location, layout string) string {
	t, err := time.Parse(time.RFC3339, stored)
	if err != nil {
		return stored
	}

	return t.In(loc).Format(layout)
}
