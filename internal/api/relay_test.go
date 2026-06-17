package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/relay"
)

// relayReq builds a request with a loopback Host so it passes guardOrigin.
func relayReq(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.Host = "localhost"
	return r
}

type relayStatus struct {
	Enabled   bool   `json:"enabled"`
	URL       string `json:"url"`
	TokenSet  bool   `json:"tokenSet"`
	Connected bool   `json:"connected"`
	DaemonID  string `json:"daemonId"`
	LastError string `json:"lastError"`
	Devices   []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"devices"`
}

func TestRelaySettingsRoundTrip(t *testing.T) {
	st, h := newTestServerWithStore(t)

	// Defaults: disabled, nothing stored.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, relayReq("GET", "/api/relay", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/relay: %d %s", rec.Code, rec.Body)
	}
	var got relayStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.TokenSet || got.Connected {
		t.Fatalf("expected pristine relay state, got %+v", got)
	}

	// Configure (enabled=false so no dial is attempted in tests).
	body := `{"enabled":false,"url":"https://relay.example.com","token":"frk_secret"}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, relayReq("PUT", "/api/relay", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/relay: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://relay.example.com" || !got.TokenSet {
		t.Fatalf("settings not persisted: %+v", got)
	}
	if strings.Contains(rec.Body.String(), "frk_secret") {
		t.Fatal("token must never be echoed back")
	}
	if v, _ := st.Settings.Get(relay.SettingToken); v != "frk_secret" {
		t.Fatalf("token not stored: %q", v)
	}

	// Empty token on update keeps the stored one.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, relayReq("PUT", "/api/relay", strings.NewReader(`{"enabled":false,"url":"https://relay.example.com","token":""}`)))
	if v, _ := st.Settings.Get(relay.SettingToken); v != "frk_secret" {
		t.Fatalf("empty token should keep stored token, got %q", v)
	}
}

func TestRelayPairRequiresConnection(t *testing.T) {
	_, h := newTestServerWithStore(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, relayReq("POST", "/api/relay/pair", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("pairing while disabled should 409, got %d %s", rec.Code, rec.Body)
	}
}

func TestRelayDeviceDelete(t *testing.T) {
	_, h := newTestServerWithStore(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, relayReq("DELETE", "/api/relay/devices/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleting unknown device should 404, got %d", rec.Code)
	}
}

// TestRelayQRPayload checks the full pairing payload contract the PWA parses.
func TestRelayQRPayload(t *testing.T) {
	p := relay.PairingPayload{
		V: 1, Portal: "https://relay.example.com", Daemon: strings.Repeat("ab", 32),
		DPK:    base64.StdEncoding.EncodeToString(make([]byte, 32)),
		Secret: base64.StdEncoding.EncodeToString(make([]byte, 16)),
		Exp:    1750000000, Name: "proj",
	}
	raw, _ := json.Marshal(p)
	frag := base64.RawURLEncoding.EncodeToString(raw)
	decoded, err := base64.RawURLEncoding.DecodeString(frag)
	if err != nil {
		t.Fatal(err)
	}
	var back relay.PairingPayload
	if err := json.Unmarshal(decoded, &back); err != nil {
		t.Fatal(err)
	}
	if back != p {
		t.Fatalf("payload round trip mismatch: %+v", back)
	}
}
