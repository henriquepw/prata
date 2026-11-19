package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestWebDevices_ListsKeyedDevicesWithoutTheWebDevice(t *testing.T) {
	w := newWebClient(t)
	phone := signinClient(t, w.client, newSignin().DeviceID)
	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Mercado", "GREEN", "0"))
	webDeviceID := w.newOps()[0].DeviceID

	page := get(w.handler, "/devices", w.cookie)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "laptop") || !strings.Contains(body, "phone") ||
		!strings.Contains(body, "/devices/"+w.deviceID) || !strings.Contains(body, "/devices/"+phone.deviceID) {
		t.Errorf("list = %d, want laptop and phone with revoke buttons: %s", page.Code, body)
	}

	if strings.Contains(body, webDeviceID) || strings.Count(body, `hx-delete="/devices/`) != 2 {
		t.Errorf("list shows the hidden web device or more than two devices: %s", body)
	}

	if code := w.submit(http.MethodDelete, "/devices/"+webDeviceID, nil).Code; code != http.StatusNotFound {
		t.Errorf("revoke web device = %d, want %d", code, http.StatusNotFound)
	}

	w.mustSubmit(http.MethodPost, "/categories", categoryForm("Feira", "GREEN", "0"))
}

func TestWebDevices_RevokeRefusesOnlyThatKey(t *testing.T) {
	w := newWebClient(t)
	phone := signinClient(t, w.client, newSignin().DeviceID)

	w.mustSubmit(http.MethodDelete, "/devices/"+phone.deviceID, nil)
	if code := phone.pullAs(phone.deviceID, 0, 10).Code; code != http.StatusUnauthorized {
		t.Errorf("pull with revoked key = %d, want %d", code, http.StatusUnauthorized)
	}

	if code := w.pullAs(w.deviceID, 0, 10).Code; code != http.StatusOK {
		t.Errorf("pull with the other device's key = %d, want %d", code, http.StatusOK)
	}

	if body := get(w.handler, "/devices", w.cookie).Body.String(); strings.Contains(body, phone.deviceID) {
		t.Errorf("list after revoke still shows the phone: %s", body)
	}

	if code := w.submit(http.MethodDelete, "/devices/"+phone.deviceID, nil).Code; code != http.StatusNotFound {
		t.Errorf("revoking again = %d, want %d", code, http.StatusNotFound)
	}
}
