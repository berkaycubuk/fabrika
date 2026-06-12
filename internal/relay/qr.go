package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// PairingPayload is what a pairing QR encodes, packed into the URL fragment
// of the portal's PWA so it opens directly on the phone and never reaches the
// portal server (fragments aren't sent in requests).
type PairingPayload struct {
	V      int    `json:"v"`
	Portal string `json:"portal"` // portal base URL
	Daemon string `json:"daemon"` // 64-hex daemon id
	DPK    string `json:"dpk"`    // daemon static public key, base64
	Secret string `json:"secret"` // one-time pairing secret, base64
	Exp    int64  `json:"exp"`    // unix expiry
	Name   string `json:"name"`   // project name, labels the entry on the phone
}

// Pairing is a freshly minted QR pairing offer for the local UI.
type Pairing struct {
	URL       string    `json:"url"` // the QR contents, also shown for copy/paste
	PNG       []byte    `json:"png"` // rendered QR code
	ExpiresAt time.Time `json:"expiresAt"`
}

// NewPairing mints a one-time pairing secret (replacing any active one) and
// renders the QR the phone scans. Fails when the relay isn't connected — a QR
// pointing at a dead tunnel would only confuse.
func (m *Manager) NewPairing() (Pairing, error) {
	st := m.Status()
	if !st.Enabled {
		return Pairing{}, errors.New("relay is disabled")
	}
	if !st.Connected {
		return Pairing{}, errors.New("relay is not connected to the portal")
	}
	identity, daemonID, err := m.ensureIdentity()
	if err != nil {
		return Pairing{}, err
	}
	_, portalURL, _, err := m.settings()
	if err != nil {
		return Pairing{}, err
	}
	secret, expires, err := m.pair.New()
	if err != nil {
		return Pairing{}, err
	}

	payload, err := json.Marshal(PairingPayload{
		V:      1,
		Portal: strings.TrimRight(portalURL, "/"),
		Daemon: daemonID,
		DPK:    base64.StdEncoding.EncodeToString(identity.Pub[:]),
		Secret: base64.StdEncoding.EncodeToString(secret),
		Exp:    expires.Unix(),
		Name:   m.projectName,
	})
	if err != nil {
		return Pairing{}, err
	}
	url := strings.TrimRight(portalURL, "/") + "/app/#pair=" + base64.RawURLEncoding.EncodeToString(payload)

	png, err := qrcode.Encode(url, qrcode.Medium, 320)
	if err != nil {
		return Pairing{}, err
	}
	return Pairing{URL: url, PNG: png, ExpiresAt: expires}, nil
}
