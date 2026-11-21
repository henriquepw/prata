package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// withPluggy adds the Pluggy credentials to the settings form.
func withPluggy(form url.Values, clientID, clientSecret string) url.Values {
	form.Set("pluggy_client_id", clientID)
	form.Set("pluggy_client_secret", clientSecret)

	return form
}

func TestWebPluggyCredentials_HiddenWithoutTheEncryptionKey(t *testing.T) {
	w := newWebClientWith(t, config{pluggyURL: newFakePluggy(t).URL})

	if body := w.submit(http.MethodGet, "/settings", nil).Body.String(); strings.Contains(body, "Pluggy") || strings.Contains(body, "pluggy_client_id") {
		t.Errorf("settings without the encryption key shows the Pluggy credentials: %s", body)
	}

	w = newWebClientWith(t, newFakePluggy(t).config())
	w.assertSettings("Pluggy", `name="pluggy_client_id"`, `name="pluggy_client_secret"`, "meu.pluggy.ai")
}

func TestWebPluggyCredentials_SavedEncryptedAndOnlyForThatUser(t *testing.T) {
	w := newWebClientWith(t, newFakePluggy(t).config())

	w.mustSubmit(http.MethodPost, "/settings", withPluggy(settingsForm("DD/MM/YYYY", "1.234,56"), pluggyClientID, pluggyClientSecret))
	w.assertSettings(`value="`+pluggyClientID+`"`, "vazio = manter o salvo", "Apagar credenciais")
	if body := w.submit(http.MethodGet, "/settings", nil).Body.String(); strings.Contains(body, pluggyClientSecret) {
		t.Errorf("settings shows the saved secret: %s", body)
	}

	if files := w.databaseFiles(); strings.Contains(files, pluggyClientSecret) || strings.Contains(files, pluggyClientID) {
		t.Error("the database holds the Pluggy credentials in plain text")
	}

	mate := w.newAccountMate()
	if body := mate.submit(http.MethodGet, "/settings", nil).Body.String(); strings.Contains(body, pluggyClientID) || strings.Contains(body, "Apagar credenciais") {
		t.Errorf("another user of the account sees the credentials: %s", body)
	}
}

func TestWebPluggyCredentials_RefusedByPluggyIs422AndSavesNothing(t *testing.T) {
	w := newWebClientWith(t, newFakePluggy(t).config())

	for name, form := range map[string]url.Values{
		"wrong secret":      withPluggy(settingsForm("YYYY-MM-DD", "1.234,56"), pluggyClientID, "outro-secret"),
		"wrong client id":   withPluggy(settingsForm("YYYY-MM-DD", "1.234,56"), "outro-client", pluggyClientSecret),
		"missing client id": withPluggy(settingsForm("YYYY-MM-DD", "1.234,56"), "", pluggyClientSecret),
	} {
		t.Run(name, func(t *testing.T) {
			response := w.submit(http.MethodPost, "/settings", form)
			if body := response.Body.String(); response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `aria-invalid="true"`) {
				t.Errorf("POST /settings = %d, want %d with the field flagged: %s", response.Code, http.StatusUnprocessableEntity, body)
			}

			w.assertSelected("DD/MM/YYYY")
			if body := w.submit(http.MethodGet, "/settings", nil).Body.String(); strings.Contains(body, "Apagar credenciais") {
				t.Errorf("refused credentials were saved: %s", body)
			}
		})
	}

	response := w.submit(http.MethodPost, "/settings", withPluggy(settingsForm("YYYY-MM-DD", "1.234,56"), pluggyClientID, "outro-secret"))
	if !strings.Contains(response.Body.String(), "A Pluggy recusou essas credenciais.") {
		t.Errorf("refused credentials lack the error: %s", response.Body)
	}
}

func TestWebPluggyCredentials_ReplacedAndDeleted(t *testing.T) {
	fake := newFakePluggy(t)
	w := newWebClientWith(t, fake.config())
	w.mustSubmit(http.MethodPost, "/settings", withPluggy(settingsForm("DD/MM/YYYY", "1.234,56"), pluggyClientID, pluggyClientSecret))

	w.mustSubmit(http.MethodPost, "/settings", withPluggy(settingsForm("YYYY-MM-DD", "1.234,56"), "client-ignorado", ""))
	w.assertSelected("YYYY-MM-DD")
	w.assertSettings(`value="`+pluggyClientID+`"`, "Apagar credenciais")

	deleted := w.submit(http.MethodDelete, "/settings/pluggy", nil)
	if body := deleted.Body.String(); deleted.Code != http.StatusOK || strings.Contains(body, pluggyClientID) || !strings.Contains(body, `name="pluggy_client_id"`) {
		t.Errorf("DELETE /settings/pluggy = %d, want %d with the sheet without the credentials: %s", deleted.Code, http.StatusOK, body)
	}

	if body := w.submit(http.MethodGet, "/settings", nil).Body.String(); strings.Contains(body, pluggyClientID) || strings.Contains(body, "Apagar credenciais") {
		t.Errorf("settings still shows the deleted credentials: %s", body)
	}
}

// databaseFiles is the raw content of the database file and its write-ahead log.
func (w *webClient) databaseFiles() string {
	w.t.Helper()
	var path string
	if err := w.db.Get(&path, "SELECT file FROM pragma_database_list WHERE name = 'main'"); err != nil {
		w.t.Fatal(err)
	}

	var files strings.Builder
	for _, name := range []string{path, path + "-wal"} {
		content, err := os.ReadFile(name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			w.t.Fatal(err)
		}

		files.Write(content)
	}

	return files.String()
}
