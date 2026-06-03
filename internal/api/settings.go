package api

import "net/http"

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.Settings.All()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if all == nil {
		all = map[string]string{}
	}
	writeJSON(w, http.StatusOK, all)
}

// putSettings upserts a flat map of string settings (role assignment, per-tier
// routing, WIP cap). Keys are merged, not replaced wholesale.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for k, v := range in {
		if err := s.store.Settings.Set(k, v); err != nil {
			mapStoreErr(w, err)
			return
		}
	}
	s.getSettings(w, r)
}
