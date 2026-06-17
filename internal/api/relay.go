package api

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/relay"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// getRelay reports relay status + paired devices for the settings UI. The
// token itself is never echoed back, only whether one is stored.
func (s *Server) getRelay(w http.ResponseWriter, _ *http.Request) {
	st := s.relay.Status()
	url, _ := s.store.Settings.Get(relay.SettingURL)
	if url == "" {
		url = relay.DefaultURL
	}
	token, _ := s.store.Settings.Get(relay.SettingToken)

	devices := []model.RelayDevice{}
	if st.DaemonID != "" {
		if ds, err := s.store.Relay.Devices(st.DaemonID); err == nil && ds != nil {
			devices = ds
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":   st.Enabled,
		"url":       url,
		"tokenSet":  token != "",
		"connected": st.Connected,
		"daemonId":  st.DaemonID,
		"sessions":  st.Sessions,
		"lastError": st.LastError,
		"devices":   devices,
	})
}

// putRelay updates relay settings and reconnects. An empty token keeps the
// stored one (the UI never sees it to send it back).
func (s *Server) putRelay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool   `json:"enabled"`
		URL     string `json:"url"`
		Token   string `json:"token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := "off"
	if body.Enabled {
		enabled = "on"
	}
	updates := map[string]string{
		relay.SettingEnabled: enabled,
		relay.SettingURL:     strings.TrimSpace(body.URL),
	}
	if t := strings.TrimSpace(body.Token); t != "" {
		updates[relay.SettingToken] = t
	}
	for k, v := range updates {
		if err := s.store.Settings.Set(k, v); err != nil {
			mapStoreErr(w, err)
			return
		}
	}
	s.relay.Reload()
	s.getRelay(w, r)
}

// pairRelay mints a one-time pairing QR. 409 when the tunnel isn't up — a QR
// pointing at an offline daemon can't pair anything.
func (s *Server) pairRelay(w http.ResponseWriter, _ *http.Request) {
	p, err := s.relay.NewPairing()
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":       p.URL,
		"png":       base64.StdEncoding.EncodeToString(p.PNG),
		"expiresAt": p.ExpiresAt,
	})
}

// deleteRelayDevice unpairs a phone (and drops its push subscriptions).
func (s *Server) deleteRelayDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Relay.DeleteDevice(r.PathValue("id")); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "device not found")
			return
		}
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
