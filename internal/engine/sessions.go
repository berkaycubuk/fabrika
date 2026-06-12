package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/google/uuid"
)

// Interactive sessions: a chat with a coding agent in its own worktree, the
// in-UI replacement for ad-hoc terminal work. Each turn replays the transcript
// through the agent's one-shot Command (no long-lived process, so an idle
// session costs nothing); Finish routes the accumulated work through the same
// commit → gate → merge pipeline tasks use. See SPECS.md §16.

// sessionTitleMax bounds the title derived from the first user message.
const sessionTitleMax = 80

// sessionRunInfo records an in-flight chat turn, held in Engine.sessRuns under
// sessMu. cancel kills the turn's subprocess (Discard mid-turn); the rest feeds
// session heartbeats and the boot reaper's active-run record.
type sessionRunInfo struct {
	agentID   string
	agentName string
	cancel    context.CancelFunc
	startedAt time.Time
}

// sessionStreamInterval is the coalescing window for "session.stream" events:
// stdout chunks arriving within it are batched into one emit, so a chatty agent
// streams live without flooding every connected UI client per write.
const sessionStreamInterval = 250 * time.Millisecond

// sessionStream accumulates the in-flight turn's stdout for streaming, held in
// Engine.sessStreams under sessMu and dropped with the run record.
type sessionStream struct {
	buf     strings.Builder
	pending bool   // a coalescing flush is already scheduled
	sent    string // last text emitted, to skip no-op flushes
}

// CreateSession opens a new interactive session: a fresh worktree on its own
// branch off base (the current branch when base is empty), ready for chat
// turns. The model, when set, overrides the agent's default for every turn.
func (e *Engine) CreateSession(agentID, modelID, base string) (*model.Session, error) {
	ag, err := e.store.Agents.Get(agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	if !ag.Enabled {
		return nil, fmt.Errorf("agent %q is disabled", ag.Name)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	repo, err := git.Open(e.ctx, e.repoRoot)
	if err != nil {
		return nil, err
	}
	if !repo.HasCommits(e.ctx) {
		return nil, fmt.Errorf("repository has no commits yet — make an initial commit first")
	}
	if base == "" {
		if base, err = repo.CurrentBranch(e.ctx); err != nil {
			return nil, err
		}
	}

	s := &model.Session{
		ID:         uuid.NewString(),
		AgentID:    ag.ID,
		Model:      modelID,
		BaseBranch: base,
		Status:     model.SessionActive,
	}
	s.Branch = "fabrika/session-" + shortID(s.ID)
	wt := e.sessionWorktreePath(s.ID)

	// Defensive cleanup mirrors claim(): a uuid collision is impossible, but a
	// stale dir from a crashed create isn't.
	_ = repo.RemoveWorktree(e.ctx, wt)
	_ = os.RemoveAll(wt)
	_ = repo.DeleteBranch(e.ctx, s.Branch)
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return nil, err
	}
	if err := repo.AddWorktree(e.ctx, wt, s.Branch, base); err != nil {
		return nil, fmt.Errorf("add worktree: %w", err)
	}
	if err := e.store.Sessions.Create(s); err != nil {
		_ = repo.RemoveWorktree(e.ctx, wt)
		return nil, err
	}
	e.emitSession(s.ID)
	log.Printf("engine: session opened with agent %q on %s (base %s)", ag.Name, s.Branch, base)
	return s, nil
}

// SendSessionMessage records the human's message (text and/or image
// attachments) and starts one chat turn: the agent runs once over the full
// transcript in the session's worktree, and its stdout becomes the reply. At
// most one turn runs per session.
func (e *Engine) SendSessionMessage(sessionID, body string, attachments []string) (*model.SessionMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" && len(attachments) == 0 {
		return nil, fmt.Errorf("message is empty")
	}
	s, err := e.store.Sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if s.Status != model.SessionActive {
		return nil, fmt.Errorf("session is %s, not active", s.Status)
	}
	ag, err := e.store.Agents.Get(s.AgentID)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	if s.Model != "" {
		ag.Model = s.Model
	}

	e.sessMu.Lock()
	if _, busy := e.sessRuns[sessionID]; busy {
		e.sessMu.Unlock()
		return nil, fmt.Errorf("a turn is already running — wait for the agent's reply")
	}
	turnCtx, cancel := context.WithCancel(e.ctx)
	e.sessRuns[sessionID] = sessionRunInfo{agentID: ag.ID, agentName: ag.Name, cancel: cancel, startedAt: time.Now()}
	e.sessMu.Unlock()

	msg := &model.SessionMessage{SessionID: sessionID, Role: model.SessionRoleUser, Body: body, Attachments: attachments}
	if err := e.store.Sessions.AddMessage(msg); err != nil {
		cancel()
		e.clearSessionRun(sessionID)
		return nil, err
	}
	if s.Title == "" && body != "" {
		_ = e.store.Sessions.SetTitle(sessionID, sessionTitle(body))
	}
	e.emit("session.message.added", *msg)
	e.emitSession(sessionID)

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer cancel()
		e.runSessionTurn(turnCtx, *s, *ag)
	}()
	return msg, nil
}

// runSessionTurn executes one chat turn and appends the agent's reply (or a
// system note on failure) to the transcript.
func (e *Engine) runSessionTurn(ctx context.Context, s model.Session, ag model.Agent) {
	defer e.clearSessionRun(s.ID)
	defer func() {
		if err := e.store.ActiveRuns.Delete(s.ID); err != nil {
			log.Printf("engine: delete active session run %s: %v", s.ID, err)
		}
	}()

	transcript, err := e.store.Sessions.Messages(s.ID)
	if err != nil {
		e.addSessionSystemMessage(s.ID, "internal error reading the transcript: "+err.Error())
		return
	}
	wt := e.sessionWorktreePath(s.ID)
	// Stage each message's image attachments into the worktree (the agent is
	// sandboxed to it) so the prompt can reference them as readable files.
	staged := map[string][]string{}
	for _, m := range transcript {
		if len(m.Attachments) > 0 {
			staged[m.ID] = e.stageAttachments(wt, m.Attachments)
		}
	}
	conventions, _ := e.store.Conventions.List()
	promptFile, cleanup, err := writeTempPrompt(agent.RenderChatPrompt(transcript, conventions, staged))
	if err != nil {
		e.addSessionSystemMessage(s.ID, "internal error writing the prompt: "+err.Error())
		return
	}
	defer cleanup()

	res, runErr := e.sessAgent.Run(ctx, ag, model.Task{ID: s.ID, Title: s.Title}, wt, promptFile)

	// The turn was cancelled: either the session was discarded mid-turn (we own
	// the worktree cleanup the discarder skipped) or fabrika is shutting down
	// (leave everything; the session reopens idle on the next boot).
	if ctx.Err() != nil {
		if cur, gerr := e.store.Sessions.Get(s.ID); gerr == nil && cur.Status == model.SessionClosed {
			e.cleanupSessionWorktree(s.ID, s.Branch)
		}
		return
	}
	switch {
	case res.Stalled:
		e.addSessionSystemMessage(s.ID, fmt.Sprintf("The agent produced no output for %s and was killed as stalled. Send another message to retry.", res.IdleFor))
	case res.TimedOut:
		e.addSessionSystemMessage(s.ID, "The agent hit its hard timeout and was killed. Send another message to retry.")
	case runErr != nil:
		e.addSessionSystemMessage(s.ID, "Agent error: "+runErr.Error())
	default:
		reply := agent.CleanChatReply(res.Stdout)
		if reply == "" {
			reply = agent.CleanChatReply(res.Stderr)
		}
		if reply == "" {
			e.addSessionSystemMessage(s.ID, "The agent finished without producing a reply.")
			break
		}
		m := &model.SessionMessage{SessionID: s.ID, Role: model.SessionRoleAgent, Body: reply}
		if err := e.store.Sessions.AddMessage(m); err != nil {
			log.Printf("engine: add session reply: %v", err)
			break
		}
		e.emit("session.message.added", *m)
	}
	e.emitSession(s.ID)
}

// FinishSession commits whatever the session's worktree holds, runs the
// verification gate, and merges into the base branch — the same exit tasks
// take. A red gate or merge conflict reopens the session with a system note so
// the human's next message can steer the fix.
func (e *Engine) FinishSession(sessionID string) error {
	s, err := e.store.Sessions.Get(sessionID)
	if err != nil {
		return err
	}
	if s.Status != model.SessionActive {
		return fmt.Errorf("session is %s, not active", s.Status)
	}
	e.sessMu.Lock()
	if _, busy := e.sessRuns[sessionID]; busy {
		e.sessMu.Unlock()
		return fmt.Errorf("a turn is still running — wait for the agent's reply")
	}
	e.sessMu.Unlock()

	e.setSessionStatus(sessionID, model.SessionGating)
	e.emitSession(sessionID)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.finishSession(*s)
	}()
	return nil
}

// finishSession is the slow half of Finish: commit, gate, merge.
func (e *Engine) finishSession(s model.Session) {
	wt := e.sessionWorktreePath(s.ID)
	reopen := func(note string) {
		e.setSessionStatus(s.ID, model.SessionActive)
		e.addSessionSystemMessage(s.ID, note)
		e.emitSession(s.ID)
	}

	var diff string
	e.mu.Lock()
	repo, err := git.Open(e.ctx, e.repoRoot)
	if err == nil {
		title := s.Title
		if title == "" {
			title = "interactive session"
		}
		if _, cerr := repo.AddAllAndCommit(e.ctx, wt, "fabrika session: "+title); cerr != nil {
			log.Printf("engine: session auto-commit: %v", cerr)
		}
		if nerr := repo.NormalizeCommitTrailers(e.ctx, s.BaseBranch, s.Branch); nerr != nil {
			log.Printf("engine: session normalize trailers: %v", nerr)
		}
		diff, _ = repo.Diff(e.ctx, s.BaseBranch, s.Branch)
	}
	e.mu.Unlock()
	if err != nil {
		reopen("Finish failed: " + err.Error())
		return
	}
	if strings.TrimSpace(diff) == "" {
		reopen("Nothing to merge — the session produced no changes.")
		return
	}

	// Verification gate: the project verbs only (sessions have no authored
	// acceptance contract — the conversation is the spec).
	ev, gerr := e.gate.Run(e.ctx, wt, e.cfg.Verbs, nil)
	if gerr != nil {
		log.Printf("engine: session gate: %v", gerr)
	}
	ev.Diff = diff
	if evJSON, jerr := json.Marshal(ev); jerr == nil {
		_ = e.store.Sessions.SetEvidence(s.ID, string(evJSON))
	}
	if !gatePassed(ev) {
		reopen("The gate failed — fix it in this session and Finish again.\n\n" + sessionGateFailureSummary(ev))
		return
	}

	e.mu.Lock()
	merr := repo.Merge(e.ctx, s.BaseBranch, s.Branch)
	if merr == nil {
		_ = repo.RemoveWorktree(e.ctx, wt)
	}
	e.mu.Unlock()
	if merr != nil {
		reopen("Merge failed (likely a conflict with newer work on " + s.BaseBranch + "): " + merr.Error())
		return
	}

	e.setSessionStatus(s.ID, model.SessionMerged)
	e.addSessionSystemMessage(s.ID, "Gate passed — merged into "+s.BaseBranch+".")
	e.emitSession(s.ID)
	log.Printf("engine: session %q merged into %s", s.Title, s.BaseBranch)
}

// DiscardSession abandons a session: any in-flight turn is killed, the worktree
// and branch are dropped, and the transcript is kept for reference. Merged and
// already-closed sessions can't be discarded; a gating one must finish first.
func (e *Engine) DiscardSession(sessionID string) error {
	s, err := e.store.Sessions.Get(sessionID)
	if err != nil {
		return err
	}
	switch s.Status {
	case model.SessionActive:
	case model.SessionGating:
		return fmt.Errorf("finish is in flight — wait for it to complete")
	default:
		return fmt.Errorf("session is already %s", s.Status)
	}

	e.sessMu.Lock()
	run, busy := e.sessRuns[sessionID]
	e.sessMu.Unlock()

	e.setSessionStatus(sessionID, model.SessionClosed)
	if busy {
		// Kill the turn; its goroutine sees the closed status and cleans up the
		// worktree once the subprocess is fully dead (avoids deleting files out
		// from under a dying process).
		run.cancel()
	} else {
		e.cleanupSessionWorktree(sessionID, s.Branch)
	}
	e.emitSession(sessionID)
	log.Printf("engine: session %q discarded", s.Title)
	return nil
}

// SessionBusy reports whether a chat turn is currently running for the session.
func (e *Engine) SessionBusy(sessionID string) bool {
	e.sessMu.Lock()
	defer e.sessMu.Unlock()
	_, busy := e.sessRuns[sessionID]
	return busy
}

// --- internals ---

func (e *Engine) sessionWorktreePath(sessionID string) string {
	return filepath.Join(e.repoRoot, ".fabrika", "worktrees", "session-"+sessionID)
}

func (e *Engine) clearSessionRun(sessionID string) {
	e.sessMu.Lock()
	delete(e.sessRuns, sessionID)
	delete(e.sessStreams, sessionID)
	e.sessMu.Unlock()
}

// onSessionOutput receives each stdout chunk of an in-flight chat turn (from
// the runner's copier goroutine) and schedules a coalesced "session.stream"
// emit, so the chat shows the reply forming instead of waiting for the turn to
// end. Chunks for a session with no tracked run are dropped (the turn finished
// between the agent's write and this call).
func (e *Engine) onSessionOutput(sessionID string, chunk []byte) {
	e.sessMu.Lock()
	if _, ok := e.sessRuns[sessionID]; !ok {
		e.sessMu.Unlock()
		return
	}
	st := e.sessStreams[sessionID]
	if st == nil {
		st = &sessionStream{}
		e.sessStreams[sessionID] = st
	}
	st.buf.Write(chunk)
	schedule := !st.pending
	st.pending = true
	e.sessMu.Unlock()
	if schedule {
		time.AfterFunc(sessionStreamInterval, func() { e.flushSessionStream(sessionID) })
	}
}

// flushSessionStream emits the in-flight turn's stdout-so-far as a
// "session.stream" event. Only complete lines are streamed (a half-written
// sentinel like fabrika_USAGE: must never flash in the UI before
// CleanChatReply can drop it), and the same cleaning as the final reply
// applies, so the streamed text is a prefix of what the transcript will show.
func (e *Engine) flushSessionStream(sessionID string) {
	e.sessMu.Lock()
	st, ok := e.sessStreams[sessionID]
	if !ok {
		e.sessMu.Unlock()
		return
	}
	st.pending = false
	raw := st.buf.String()
	text := ""
	if i := strings.LastIndexByte(raw, '\n'); i >= 0 {
		text = agent.CleanChatReply(raw[:i])
	}
	if text == "" || text == st.sent {
		e.sessMu.Unlock()
		return
	}
	st.sent = text
	agentName := e.sessRuns[sessionID].agentName
	e.sessMu.Unlock()
	e.emit("session.stream", map[string]any{
		"sessionId": sessionID,
		"agentName": agentName,
		"text":      text,
	})
}

func (e *Engine) cleanupSessionWorktree(sessionID, branch string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	wt := e.sessionWorktreePath(sessionID)
	if repo, err := git.Open(e.ctx, e.repoRoot); err == nil {
		_ = repo.RemoveWorktree(e.ctx, wt)
		if branch != "" {
			_ = repo.DeleteBranch(e.ctx, branch)
		}
	}
	_ = os.RemoveAll(wt)
}

func (e *Engine) setSessionStatus(id, status string) {
	if err := e.store.Sessions.SetStatus(id, status); err != nil {
		log.Printf("engine: set session status %s=%s: %v", id, status, err)
	}
}

// addSessionSystemMessage appends a system note to the transcript and emits it.
func (e *Engine) addSessionSystemMessage(id, body string) {
	m := &model.SessionMessage{SessionID: id, Role: model.SessionRoleSystem, Body: body}
	if err := e.store.Sessions.AddMessage(m); err != nil {
		log.Printf("engine: add session system message: %v", err)
		return
	}
	e.emit("session.message.added", *m)
}

// emitSession broadcasts the session's current state (with the transient busy
// flag) so the UI live-updates.
func (e *Engine) emitSession(id string) {
	s, err := e.store.Sessions.Get(id)
	if err != nil {
		return
	}
	s.Busy = e.SessionBusy(id)
	e.emit("session.updated", *s)
}

// onSessionHeartbeat mirrors onHeartbeat for chat turns: a liveness pulse so
// the chat shows the agent working (or fallen quiet) between messages.
func (e *Engine) onSessionHeartbeat(hb agent.HeartbeatInfo) {
	e.sessMu.Lock()
	ri, ok := e.sessRuns[hb.TaskID]
	e.sessMu.Unlock()
	if !ok {
		return
	}
	e.emit("session.heartbeat", map[string]any{
		"sessionId":      hb.TaskID,
		"agentName":      hb.AgentName,
		"idleSeconds":    int(hb.IdleFor.Round(time.Second) / time.Second),
		"lastLine":       hb.LastLine,
		"outputBytes":    hb.OutputBytes,
		"runningSeconds": int(time.Since(ri.startedAt).Round(time.Second) / time.Second),
	})
}

// onSessionAgentStart records the turn's pgid so the boot reaper can kill a
// subprocess orphaned by a crash, same as task runs.
func (e *Engine) onSessionAgentStart(sessionID string, pgid int) {
	e.sessMu.Lock()
	ri, ok := e.sessRuns[sessionID]
	e.sessMu.Unlock()
	if !ok {
		return
	}
	if err := e.store.ActiveRuns.Record(sessionID, pgid, ri.agentID); err != nil {
		log.Printf("engine: record active session run %s pgid=%d: %v", sessionID, pgid, err)
	}
}

// sessionGateFailureSummary reports each failing gate stage with the tail of
// its output, for the reopen system note.
func sessionGateFailureSummary(ev model.Evidence) string {
	var b strings.Builder
	for _, stage := range stageOrder {
		res, ok := ev.Stages[stage]
		if !ok || res.Pass || res.Skipped {
			continue
		}
		fmt.Fprintf(&b, "stage %q failed:\n%s\n\n", stage, tailLines(res.Output, 40))
	}
	return strings.TrimSpace(b.String())
}

// sessionTitle derives a session title from the first user message: its first
// line, truncated.
func sessionTitle(body string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(body), "\n", 2)[0])
	r := []rune(line)
	if len(r) > sessionTitleMax {
		return string(r[:sessionTitleMax]) + "…"
	}
	return line
}
