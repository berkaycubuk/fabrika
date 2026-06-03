package api

import (
	"net/http"
	"strconv"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// AgentMetrics is per-agent live activity + trust signals (SPECS §10, §14).
type AgentMetrics struct {
	AgentID      string  `json:"agentId"`
	Name         string  `json:"name"`
	Enabled      bool    `json:"enabled"`
	Concurrency  int     `json:"concurrency"`
	Running      int     `json:"running"`      // tasks in flight (running/verifying)
	Merged       int     `json:"merged"`       // shipped by this agent
	KickedBack   int     `json:"kickedBack"`   // rejected (closed) PRs
	KickbackRate float64 `json:"kickbackRate"` // kicked / (merged + kicked)
}

// Metrics is the engine-room snapshot: per-agent activity plus board totals.
type Metrics struct {
	Agents     []AgentMetrics `json:"agents"`
	WIP        int            `json:"wip"`        // tasks currently running/verifying
	WIPCap     int            `json:"wipCap"`     // configured ceiling (0 = unlimited)
	Ready      int            `json:"ready"`      // queued, awaiting an agent slot
	InReview   int            `json:"inReview"`   // awaiting human accept
	Blocked    int            `json:"blocked"`    // escalated/blocked
	Merged     int            `json:"merged"`     // total shipped
	Throughput int            `json:"throughput"` // total merged (lifetime)
}

// getMetrics computes activity and trust metrics from task state. Counts are
// derived from current task status + the agent each task last ran on, so they
// reflect the live board without extra bookkeeping.
func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.Tasks.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	agents, err := s.store.Agents.List()
	if err != nil {
		mapStoreErr(w, err)
		return
	}

	per := map[string]*AgentMetrics{}
	order := []string{}
	for _, a := range agents {
		per[a.ID] = &AgentMetrics{
			AgentID: a.ID, Name: a.Name, Enabled: a.Enabled, Concurrency: a.Concurrency,
		}
		order = append(order, a.ID)
	}

	m := Metrics{}
	for _, t := range tasks {
		switch t.Status {
		case model.TaskRunning, model.TaskVerifying, model.TaskClaimed:
			m.WIP++
		case model.TaskReady:
			m.Ready++
		case model.TaskReview:
			m.InReview++
		case model.TaskBlocked, model.TaskFailed:
			m.Blocked++
		case model.TaskMerged:
			m.Merged++
			m.Throughput++
		}
		am := per[t.AgentID]
		if am == nil {
			continue // task never ran on a known agent
		}
		switch t.Status {
		case model.TaskRunning, model.TaskVerifying, model.TaskClaimed:
			am.Running++
		case model.TaskMerged:
			am.Merged++
		case model.TaskClosed:
			am.KickedBack++
		}
	}

	for _, id := range order {
		am := per[id]
		if denom := am.Merged + am.KickedBack; denom > 0 {
			am.KickbackRate = float64(am.KickedBack) / float64(denom)
		}
		m.Agents = append(m.Agents, *am)
	}
	if m.Agents == nil {
		m.Agents = []AgentMetrics{}
	}

	if v, _ := s.store.Settings.Get("wip_cap"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			m.WIPCap = n
		}
	}

	writeJSON(w, http.StatusOK, m)
}
