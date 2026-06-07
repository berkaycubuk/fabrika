package api

import (
	"net/http"

	"github.com/berkaycubuk/fabrika/internal/config"
)

// getConfig responds with the authoritative in-memory manifest the engine uses
// (s.cfg), serialized as JSON. It reads from memory, not disk. A nil cfg yields
// an empty config object.
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		writeJSON(w, http.StatusOK, &config.Config{})
		return
	}
	writeJSON(w, http.StatusOK, s.cfg)
}

// putConfig persists an updated manifest to fabrika.toml and, on success,
// mutates the shared in-memory config in place so the running engine (which
// holds the same pointer) picks up the change immediately. A config rejected by
// Save (e.g. invalid autonomy policy) yields 400 and writes nothing else.
func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var incoming config.Config
	if err := decodeJSON(r, &incoming); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.Save(s.repoRoot, &incoming); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	*s.cfg = incoming
	writeJSON(w, http.StatusOK, s.cfg)
}
