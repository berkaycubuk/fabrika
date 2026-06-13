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

// gitPush is the relay-facing push: the phone PWA calls POST /api/git/push and
// renders the branch/remote it shipped to. It mirrors pushMain's behaviour
// (same engine.Push, same 409-on-failure) but reports structured fields. The
// branch/remote come from PushStatus, which resolves them without a network
// round-trip before the push itself runs.
func (s *Server) gitPush(w http.ResponseWriter, r *http.Request) {
	st, err := s.engine.PushStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.engine.Push(r.Context()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pushed": true, "branch": st.Branch, "remote": st.Remote})
}
