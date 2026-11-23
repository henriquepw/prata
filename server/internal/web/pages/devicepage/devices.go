// Package devicepage is the Dispositivos screen and its activity log.
package devicepage

import (
	"errors"
	"net/http"

	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/components/shell"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// Page is the Dispositivos handlers.
type Page struct {
	db    *store.Store
	shell func(w http.ResponseWriter, r *http.Request, title string) (shell.Shell, bool)
}

// New returns the devices page over db; shell reads the console chrome of an authenticated page, as web's shell does.
func New(db *store.Store, shell func(w http.ResponseWriter, r *http.Request, title string) (shell.Shell, bool)) Page {
	return Page{db: db, shell: shell}
}

// Devices is the Dispositivos screen.
func (h Page) Devices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.db.Devices(r.Context(), shared.SessionFrom(r.Context()).AccountID)
	if err != nil {
		shared.InternalError(w, "listing devices", err)
		return
	}

	activity, err := h.db.Activity(r.Context(), shared.SessionFrom(r.Context()).AccountID)
	if err != nil {
		shared.InternalError(w, "reading activity", err)
		return
	}

	s, ok := h.shell(w, r, "nav.devices")
	if !ok {
		return
	}

	shared.Render(w, r, http.StatusOK, devicesPage(s, devices, activity))
}

// Revoke revokes the account's device.
func (h Page) Revoke(w http.ResponseWriter, r *http.Request) {
	err := h.db.RevokeDevice(r.Context(), shared.SessionFrom(r.Context()).AccountID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}

	if err != nil {
		shared.InternalError(w, "revoking device", err)
		return
	}

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
