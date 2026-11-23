package shell

import "net/http"

// Func builds the Shell of a page titled by the i18n key, false once it has already answered the request.
type Func func(w http.ResponseWriter, r *http.Request, title string) (Shell, bool)
