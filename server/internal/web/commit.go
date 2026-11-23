package web

import (
	"net/http"

	"github.com/a-h/templ"
	"github.com/henriquepw/prata/server/internal/store"
	"github.com/henriquepw/prata/server/internal/web/shared"
)

// commit is shared.Commit over the handler's store, the end of every sheet submit.
func (h handler) commit(w http.ResponseWriter, r *http.Request, fieldErrors map[string]string, sheet templ.Component, changes ...store.Change) {
	shared.Commit(h.db, w, r, fieldErrors, sheet, changes...)
}
