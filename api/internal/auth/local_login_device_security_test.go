package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The login-device cookie set on a successful sign-in is recognised for the
// same account only.
func TestLoginDevice_CookieRoundTrip(t *testing.T) {
	rr := httptest.NewRecorder()
	setLoginDeviceCookie(rr, "dev-alice")

	var issued *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == loginDeviceCookie {
			issued = c
		}
	}
	if issued == nil {
		t.Fatalf("no %s cookie set", loginDeviceCookie)
	}
	if !issued.HttpOnly || !issued.Secure || issued.Path != "/auth/login" || issued.MaxAge != loginDeviceTTL {
		t.Fatalf("unexpected cookie attributes: %+v", issued)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil)
	req.AddCookie(issued)
	if !knownLoginDevice(req, "dev-alice") {
		t.Fatal("issued cookie not recognised for its own account")
	}
	if knownLoginDevice(req, "dev-bob") {
		t.Fatal("issued cookie recognised for a different account")
	}
}

func TestLoginDevice_MissingOrForgedCookieUnknown(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil)
	if knownLoginDevice(req, "dev-carol") {
		t.Fatal("request without cookie treated as a known device")
	}
	req.AddCookie(&http.Cookie{Name: loginDeviceCookie, Value: "forged-value"})
	if knownLoginDevice(req, "dev-carol") {
		t.Fatal("forged cookie treated as a known device")
	}
}
