package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/henriquepw/prata/server/internal/store"
)

func (h handler) devices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.db.Devices(r.Context(), sessionFrom(r.Context()).AccountID)
	if err != nil {
		internalError(w, "listing devices", err)
		return
	}

	s, ok := h.shell(w, r, "Dispositivos")
	if !ok {
		return
	}

	render(w, r, http.StatusOK, devicesPage(s, devices))
}

func (h handler) revokeDevice(w http.ResponseWriter, r *http.Request) {
	err := h.db.RevokeDevice(r.Context(), sessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}

	if err != nil {
		internalError(w, "revoking device", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// localTime writes an RFC 3339 time in loc as "02/01/2006 15:04", or "nunca" when it is empty.
func localTime(value string, loc *time.Location) string {
	if value == "" {
		return "nunca"
	}

	return formatDate(value, loc, brDate+" 15:04")
}
