package engine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// registerSessionAgent adds an enabled agent and returns it for session tests.
func registerSessionAgent(t *testing.T, st *store.Store, command string) *model.Agent {
	t.Helper()
	a := &model.Agent{
		Name:    "fake-chat",
		Command: command,
		Roles:   []string{model.RoleImplementer},
		Enabled: true,
	}
	if err := st.Agents.Create(a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSessionChatTurnAndMerge(t *testing.T) {
	eng, st, repo := setup(t)
	// The fake agent replies on stdout and leaves a change in the worktree.
	ag := registerSessionAgent(t, st, "printf 'fixed the thing' && printf 'x' > out.txt")

	s, err := eng.CreateSession(ag.ID, "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s.Status != model.SessionActive || s.BaseBranch != "main" {
		t.Fatalf("session = %+v, want active on main", s)
	}
	if _, err := os.Stat(eng.sessionWorktreePath(s.ID)); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}

	if _, err := eng.SendSessionMessage(s.ID, "fix the thing", nil); err != nil {
		t.Fatalf("SendSessionMessage: %v", err)
	}
	// A second send while the turn runs must be rejected.
	if _, err := eng.SendSessionMessage(s.ID, "another", nil); err == nil {
		t.Fatal("expected concurrent turn to be rejected")
	}
	waitFor(t, "agent reply", func() bool {
		msgs, _ := st.Sessions.Messages(s.ID)
		return len(msgs) >= 2 && msgs[len(msgs)-1].Role == model.SessionRoleAgent
	})
	msgs, _ := st.Sessions.Messages(s.ID)
	if got := msgs[len(msgs)-1].Body; got != "fixed the thing" {
		t.Fatalf("reply = %q, want %q", got, "fixed the thing")
	}
	got, _ := st.Sessions.Get(s.ID)
	if got.Title != "fix the thing" {
		t.Fatalf("title = %q, want first message", got.Title)
	}

	waitFor(t, "turn slot to free", func() bool { return !eng.SessionBusy(s.ID) })
	if err := eng.FinishSession(s.ID); err != nil {
		t.Fatalf("FinishSession: %v", err)
	}
	waitFor(t, "merge", func() bool {
		cur, _ := st.Sessions.Get(s.ID)
		return cur.Status == model.SessionMerged
	})
	if _, err := os.Stat(filepath.Join(repo, "out.txt")); err != nil {
		t.Fatalf("merged file missing on base branch: %v", err)
	}
	if _, err := os.Stat(eng.sessionWorktreePath(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed after merge, stat err = %v", err)
	}
}

func TestSessionTurnStreamsReply(t *testing.T) {
	eng, st, _ := setup(t)
	// The fake agent prints a sentinel line plus prose, then stays alive long
	// enough for the coalesced flush (sessionStreamInterval) to fire mid-turn.
	ag := registerSessionAgent(t, st, "printf 'fabrika_COMMENT: noise\\nstreamed reply\\n'; sleep 1")

	var mu sync.Mutex
	var streams []map[string]any
	eng.emit = func(typ string, payload any) {
		if typ != "session.stream" {
			return
		}
		mu.Lock()
		streams = append(streams, payload.(map[string]any))
		mu.Unlock()
	}

	s, err := eng.CreateSession(ag.ID, "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := eng.SendSessionMessage(s.ID, "stream it", nil); err != nil {
		t.Fatalf("SendSessionMessage: %v", err)
	}

	// The stream event must land while the turn is still running, carry the
	// reply with the sentinel line already stripped, and name the agent.
	waitFor(t, "a session.stream event mid-turn", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(streams) > 0
	})
	if !eng.SessionBusy(s.ID) {
		t.Fatal("stream event arrived only after the turn ended")
	}
	mu.Lock()
	first := streams[0]
	mu.Unlock()
	if first["sessionId"] != s.ID || first["agentName"] != "fake-chat" {
		t.Fatalf("stream payload identity = %+v", first)
	}
	if got := first["text"]; got != "streamed reply" {
		t.Fatalf("streamed text = %q, want %q (sentinel lines stripped)", got, "streamed reply")
	}

	// The stream buffer is dropped with the run record once the turn ends.
	waitFor(t, "turn to end", func() bool { return !eng.SessionBusy(s.ID) })
	eng.sessMu.Lock()
	left := len(eng.sessStreams)
	eng.sessMu.Unlock()
	if left != 0 {
		t.Fatalf("sessStreams not cleaned up: %d left", left)
	}
}

func TestSessionMessageAttachmentsStagedIntoWorktree(t *testing.T) {
	eng, st, repo := setup(t)
	// The fake agent lists the staged attachments dir; its reply proves the
	// image was copied into the worktree where a sandboxed agent can read it.
	ag := registerSessionAgent(t, st, "ls .fabrika/attachments")

	uploads := filepath.Join(repo, ".fabrika", "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, "mock.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := eng.CreateSession(ag.ID, "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := eng.SendSessionMessage(s.ID, "match this mockup", []string{"/api/uploads/mock.png"}); err != nil {
		t.Fatalf("SendSessionMessage: %v", err)
	}
	waitFor(t, "agent reply", func() bool {
		msgs, _ := st.Sessions.Messages(s.ID)
		return len(msgs) >= 2 && msgs[len(msgs)-1].Role == model.SessionRoleAgent
	})
	msgs, _ := st.Sessions.Messages(s.ID)
	if got := msgs[0].Attachments; len(got) != 1 || got[0] != "/api/uploads/mock.png" {
		t.Fatalf("stored attachments = %v, want the upload URL", got)
	}
	if reply := msgs[len(msgs)-1].Body; !strings.Contains(reply, "mock.png") {
		t.Fatalf("reply = %q, want it to list the staged mock.png", reply)
	}
}

func TestSessionFinishWithNoChangesReopens(t *testing.T) {
	eng, st, _ := setup(t)
	ag := registerSessionAgent(t, st, "true")

	s, err := eng.CreateSession(ag.ID, "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := eng.FinishSession(s.ID); err != nil {
		t.Fatalf("FinishSession: %v", err)
	}
	waitFor(t, "session to reopen", func() bool {
		cur, _ := st.Sessions.Get(s.ID)
		return cur.Status == model.SessionActive
	})
	msgs, _ := st.Sessions.Messages(s.ID)
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != model.SessionRoleSystem {
		t.Fatalf("expected a system note about nothing to merge, got %+v", msgs)
	}
}

func TestSessionDiscardCleansUp(t *testing.T) {
	eng, st, _ := setup(t)
	ag := registerSessionAgent(t, st, "true")

	s, err := eng.CreateSession(ag.ID, "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := eng.DiscardSession(s.ID); err != nil {
		t.Fatalf("DiscardSession: %v", err)
	}
	cur, _ := st.Sessions.Get(s.ID)
	if cur.Status != model.SessionClosed {
		t.Fatalf("status = %q, want closed", cur.Status)
	}
	if _, err := os.Stat(eng.sessionWorktreePath(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err = %v", err)
	}
	if err := eng.DiscardSession(s.ID); err == nil {
		t.Fatal("expected double discard to be rejected")
	}
}
