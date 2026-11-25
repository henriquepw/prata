package layout

import "net/http"

// Func builds the Props of a page titled by the i18n key, false once it has already answered the request.
type Func func(w http.ResponseWriter, r *http.Request, title string) (Props, bool)
