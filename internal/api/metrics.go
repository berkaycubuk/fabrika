package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// AgentMetrics is per-agent live activity + trust signals (SPECS §10, §14).
type AgentMetrics struct {
	AgentID      string  `json:"agentId"`
	Name         string  `json:"name"`
	Enabled      bool    `json:"enabled"`
	Concurrency  int     `json:"concurrency"`
	Running      int     `json:"running"`      // tasks in flight (running/verifying)
	Planning     int     `json:"planning"`     // big-task planning runs in flight
	Merged       int     `json:"merged"`       // shipped by this agent
	KickedBack   int     `json:"kickedBack"`   // rejected (closed) PRs
	KickbackRate float64 `json:"kickbackRate"` // kicked / (merged + kicked)
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
}

// Metrics is the engine-room snapshot: per-agent activity plus board totals and
// the Phase 3 trust numbers (SPECS §14).
type Metrics struct {
	Agents     []AgentMetrics `json:"agents"`
	WIP        int            `json:"wip"`        // tasks currently running/verifying
	Planning   int            `json:"planning"`   // big tasks currently being planned
	WIPCap     int            `json:"wipCap"`     // configured ceiling (0 = unlimited)
	Ready      int            `json:"ready"`      // queued, awaiting an agent slot
	InReview   int            `json:"inReview"`   // awaiting human accept
	Blocked    int            `json:"blocked"`    // escalated/blocked
	Merged     int            `json:"merged"`     // total shipped
	Throughput int            `json:"throughput"` // total merged (lifetime)

	// Trust + autonomy (Phase 3).
	AutoMerged      int     `json:"autoMerged"`      // merged by the machine, no human accept
	ManualMerged    int     `json:"manualMerged"`    // merged via human Accept
	Reverted        int     `json:"reverted"`        // merged then marked a change-failure
	AuditQueue      int     `json:"auditQueue"`      // auto-merges awaiting a post-merge audit
	AutoMergeShare  float64 `json:"autoMergeShare"`  // autoMerged / merged — "merges without you"
	TouchesPerUnit  float64 `json:"touchesPerUnit"`  // human interventions per shipped unit (drive down)
	ChangeFailRate  float64 `json:"changeFailRate"`  // reverted / merged — keep flat as autonomy widens
	AuditRate       float64 `json:"auditRate"`       // configured post-merge audit sampling rate
	MutationTesting bool    `json:"mutationTesting"` // mutation-testing validator enabled

	TotalTokens int `json:"totalTokens"` // board-wide sum of agent token usage
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
	kickedBack := 0
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
			if t.AutoMerged {
				m.AutoMerged++
			} else {
				m.ManualMerged++
			}
			if t.Reverted {
				m.Reverted++
			}
			if t.AuditFlagged && !t.Reverted {
				m.AuditQueue++
			}
		case model.TaskClosed:
			kickedBack++
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

	// Fold in live planning activity. The planner runs outside the dispatch
	// loop, so it leaves no task in a running status — without this it reads as
	// idle while it's actually decomposing a big task.
	for id, n := range s.engine.PlanningCounts() {
		m.Planning += n
		if am := per[id]; am != nil {
			am.Planning += n
		}
	}

	// Per-agent token totals from all attempts. A failed read is non-critical —
	// agents report zeros and the board total stays 0.
	if usageByAgent, err := s.store.Attempts.TokensByAgent(); err != nil {
		log.Printf("getMetrics: TokensByAgent: %v", err)
	} else {
		for agentID, u := range usageByAgent {
			am := per[agentID]
			if am == nil {
				continue
			}
			am.InputTokens = u.InputTokens
			am.OutputTokens = u.OutputTokens
			am.TotalTokens = u.TotalTokens
			m.TotalTokens += u.TotalTokens
		}
	}

	// Trust ratios. Touches = every human intervention in the pipeline (manual
	// accepts + kick-backs + answered decisions + reverts) per shipped unit.
	answered, _ := s.store.Decisions.CountAnswered()
	if m.Merged > 0 {
		denom := float64(m.Merged)
		m.AutoMergeShare = float64(m.AutoMerged) / denom
		m.ChangeFailRate = float64(m.Reverted) / denom
		touches := m.ManualMerged + kickedBack + answered + m.Reverted
		m.TouchesPerUnit = float64(touches) / denom
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
	if v, _ := s.store.Settings.Get("audit_rate"); v != "" {
		if r, err := strconv.ParseFloat(v, 64); err == nil {
			m.AuditRate = r
		}
	}
	if v, _ := s.store.Settings.Get("mutation_testing"); v != "" {
		m.MutationTesting = strings.EqualFold(v, "on")
	}

	writeJSON(w, http.StatusOK, m)
}
