package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/agent"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// AskTaskQuestion stores the human's question as a comment, then runs the named
// agent one-shot in an ephemeral detached worktree and appends its reply to the
// task's comment thread. Works for any task status, including merged ones whose
// original worktree is long gone. At most one reply is in flight per task.
func (e *Engine) AskTaskQuestion(taskID, agentID, body string, attachments []string) (*model.Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("question body is empty")
	}

	task, err := e.store.Tasks.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("task: %w", err)
	}
	ag, err := e.store.Agents.Get(agentID)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	if !ag.Enabled {
		return nil, fmt.Errorf("agent %q is disabled", ag.Name)
	}

	e.askMu.Lock()
	if _, busy := e.askRuns[taskID]; busy {
		e.askMu.Unlock()
		return nil, fmt.Errorf("a reply is already in flight for this task — wait for the agent's answer")
	}
	e.askRuns[taskID] = struct{}{}
	e.askMu.Unlock()

	c := &model.Comment{
		TaskID:      taskID,
		AuthorType:  "user",
		AuthorID:    "",
		Body:        body,
		Attachments: attachments,
	}
	if err := e.store.Comments.Create(c); err != nil {
		e.clearAskRun(taskID)
		return nil, fmt.Errorf("create comment: %w", err)
	}
	e.emit("task.comment.added", *c)

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.clearAskRun(taskID)
		e.runQuestionTurn(e.ctx, *task, *ag)
	}()

	return c, nil
}

// runQuestionTurn creates an ephemeral detached worktree, runs the agent over
// the full comment thread, and appends the reply (or a failure note) to the
// thread.
func (e *Engine) runQuestionTurn(ctx context.Context, task model.Task, ag model.Agent) {
	wt := e.askWorktreePath(task.ID)

	// Choose a stable ref: prefer the merge commit (survives branch deletion),
	// then the task branch, then fall back to HEAD.
	ref, err := e.chooseAskRef(ctx, task)
	if err != nil {
		log.Printf("engine: ask %s: resolve ref: %v", task.ID, err)
		e.addQuestionAgentNote(task.ID, ag.ID, "Could not resolve a git ref for this task: "+err.Error())
		return
	}

	// Defensive cleanup: stale dir from a previous crashed turn.
	repo, err := git.Open(ctx, e.repoRoot)
	if err != nil {
		log.Printf("engine: ask %s: open repo: %v", task.ID, err)
		e.addQuestionAgentNote(task.ID, ag.ID, "Could not open repository: "+err.Error())
		return
	}
	_ = repo.RemoveWorktree(ctx, wt)
	_ = os.RemoveAll(wt)
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		log.Printf("engine: ask %s: mkdir: %v", task.ID, err)
		e.addQuestionAgentNote(task.ID, ag.ID, "Could not prepare worktree directory: "+err.Error())
		return
	}
	if err := repo.AddWorktreeDetached(ctx, wt, ref); err != nil {
		log.Printf("engine: ask %s: add worktree: %v", task.ID, err)
		e.addQuestionAgentNote(task.ID, ag.ID, "Could not create worktree at ref "+ref+": "+err.Error())
		return
	}
	defer func() {
		_ = repo.RemoveWorktree(ctx, wt)
		_ = os.RemoveAll(wt)
	}()

	thread, err := e.store.Comments.ListForTask(task.ID)
	if err != nil {
		log.Printf("engine: ask %s: list comments: %v", task.ID, err)
		e.addQuestionAgentNote(task.ID, ag.ID, "Could not read the comment thread: "+err.Error())
		return
	}

	promptFile, cleanup, err := writeTempPrompt(agent.RenderQuestionPrompt(model.Task{ID: task.ID, Title: task.Title, Spec: task.Spec}, thread))
	if err != nil {
		log.Printf("engine: ask %s: write prompt: %v", task.ID, err)
		e.addQuestionAgentNote(task.ID, ag.ID, "Could not write the prompt: "+err.Error())
		return
	}
	defer cleanup()

	res, runErr := e.sessAgent.Run(ctx, ag, model.Task{ID: task.ID, Title: task.Title}, wt, promptFile)
	if ctx.Err() != nil {
		return
	}

	switch {
	case res.Stalled:
		e.addQuestionAgentNote(task.ID, ag.ID, fmt.Sprintf("The agent produced no output for %s and was killed as stalled.", res.IdleFor))
	case res.TimedOut:
		e.addQuestionAgentNote(task.ID, ag.ID, "The agent hit its hard timeout and was killed.")
	case runErr != nil:
		log.Printf("engine: ask %s: run: %v", task.ID, runErr)
		e.addQuestionAgentNote(task.ID, ag.ID, "Agent error: "+runErr.Error())
	default:
		reply := agent.CleanChatReply(res.Stdout)
		if reply == "" {
			reply = agent.CleanChatReply(res.Stderr)
		}
		if reply == "" {
			e.addQuestionAgentNote(task.ID, ag.ID, "The agent finished without producing a reply.")
			return
		}
		rc := &model.Comment{TaskID: task.ID, AuthorType: "agent", AuthorID: ag.ID, Body: reply}
		if err := e.store.Comments.Create(rc); err != nil {
			log.Printf("engine: ask %s: create reply comment: %v", task.ID, err)
			return
		}
		e.emit("task.comment.added", *rc)
	}
}

// chooseAskRef returns the best git ref for a detached ephemeral worktree:
// the merge commit SHA if the task was merged, the task branch if present,
// or the current HEAD branch as a fallback.
func (e *Engine) chooseAskRef(ctx context.Context, task model.Task) (string, error) {
	if task.MergeCommitSHA != "" {
		return task.MergeCommitSHA, nil
	}
	if task.Branch != "" {
		return task.Branch, nil
	}
	repo, err := git.Open(ctx, e.repoRoot)
	if err != nil {
		return "", err
	}
	return repo.CurrentBranch(ctx)
}

// askWorktreePath returns the filesystem path for this task's ephemeral Q&A
// worktree.
func (e *Engine) askWorktreePath(taskID string) string {
	return filepath.Join(e.repoRoot, ".fabrika", "worktrees", "ask-"+taskID)
}

// addQuestionAgentNote appends a short agent-authored comment so the thread
// always shows a response even when the run failed.
func (e *Engine) addQuestionAgentNote(taskID, agentID, note string) {
	c := &model.Comment{TaskID: taskID, AuthorType: "agent", AuthorID: agentID, Body: note}
	if err := e.store.Comments.Create(c); err != nil {
		log.Printf("engine: ask %s: create note: %v", taskID, err)
		return
	}
	e.emit("task.comment.added", *c)
}

// clearAskRun removes the in-flight marker for taskID.
func (e *Engine) clearAskRun(taskID string) {
	e.askMu.Lock()
	delete(e.askRuns, taskID)
	e.askMu.Unlock()
}
