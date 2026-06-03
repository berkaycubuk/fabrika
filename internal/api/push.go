package api

import "net/http"

// pushMain ships the integration branch (the current branch that accepted tasks
// merge into) to its remote, so the human can publish the work agents have
// accumulated locally. Failures (no remote, non-fast-forward rejection, network)
// surface as 409 with git's message. Uses the request context so a disconnect
// cancels the push.
// pushStatus reports whether the integration branch has unpushed commits, so
// the UI can show the Push action only when there is something to ship.
func (s *Server) pushStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.engine.PushStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) pushMain(w http.ResponseWriter, r *http.Request) {
	summary, err := s.engine.Push(r.Context())
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pushed", "detail": summary})
}
