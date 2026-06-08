// Package ci polls a configurable command for CI run results and flags tasks
// whose pushed commits failed CI. The command emits a JSON array of Run objects;
// fabrika does not depend on any specific CI provider.
package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Run is one CI run reported by the configured command.
type Run struct {
	SHA        string `json:"sha"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

// ParseRuns unmarshals a JSON array of Run from command stdout.
// Empty or whitespace-only stdout returns zero runs and a nil error.
func ParseRuns(jsonOut string) ([]Run, error) {
	if strings.TrimSpace(jsonOut) == "" {
		return nil, nil
	}
	var runs []Run
	if err := json.Unmarshal([]byte(jsonOut), &runs); err != nil {
		return nil, fmt.Errorf("parse runs: %w", err)
	}
	return runs, nil
}

// Update describes a CI status change to apply to one task.
type Update struct {
	TaskID string
	Status string
	RunURL string
}

// Match pairs each Run with the task whose MergeCommitSHA equals run.SHA.
// Tasks with an empty MergeCommitSHA and runs with no matching task are skipped.
// Conclusion "failure" maps to Status "failure"; "success" maps to "success".
// Any other conclusion produces no Update. Results preserve input order.
func Match(runs []Run, tasks []model.Task) []Update {
	bySHA := make(map[string]string, len(tasks))
	for _, t := range tasks {
		if t.MergeCommitSHA == "" {
			continue
		}
		bySHA[t.MergeCommitSHA] = t.ID
	}
	var out []Update
	for _, r := range runs {
		taskID, ok := bySHA[r.SHA]
		if !ok {
			continue
		}
		var status string
		switch r.Conclusion {
		case "failure":
			status = "failure"
		case "success":
			status = "success"
		default:
			continue
		}
		out = append(out, Update{TaskID: taskID, Status: status, RunURL: r.URL})
	}
	return out
}

// Commander runs a command in a workdir, returning combined output and an error
// on non-zero exit. Satisfied by gate.CommandRunner and the engine's engineCommander.
type Commander interface {
	RunCommand(ctx context.Context, workdir, command string, env []string) (string, error)
}

// Deps is the dependency bundle passed to NewPoller.
type Deps struct {
	Tasks       *store.TaskRepo
	Cmd         Commander
	Command     string
	RepoRoot    string
	Emit        func(string, any)
	PollSeconds int
}

// Poller polls the CI command and updates task CI status on each tick.
type Poller struct {
	d Deps
}

// NewPoller constructs a Poller. A nil Emit is replaced with a no-op.
func NewPoller(d Deps) *Poller {
	if d.Emit == nil {
		d.Emit = func(string, any) {}
	}
	return &Poller{d: d}
}

// PollOnce runs one tick: executes the CI command, parses its output, matches
// runs to tasks, and for each match updates the CI status and emits task.updated.
// When a task transitions to "failure" for the first time a high-priority fix
// task is spawned (mirroring incident-driven fix tasks). Logs and returns any
// error without panicking.
func (p *Poller) PollOnce(ctx context.Context) error {
	out, err := p.d.Cmd.RunCommand(ctx, p.d.RepoRoot, p.d.Command, nil)
	if err != nil {
		log.Printf("ci: run command: %v", err)
		return err
	}
	runs, err := ParseRuns(out)
	if err != nil {
		log.Printf("ci: parse runs: %v", err)
		return err
	}
	tasks, err := p.d.Tasks.List()
	if err != nil {
		log.Printf("ci: list tasks: %v", err)
		return err
	}
	// Index tasks by ID for pre-update CIStatus lookup (dedup fix-task spawns).
	prevByID := make(map[string]model.Task, len(tasks))
	for _, t := range tasks {
		prevByID[t.ID] = t
	}
	updates := Match(runs, tasks)
	for _, u := range updates {
		if err := p.d.Tasks.SetCIStatus(u.TaskID, u.Status, u.RunURL); err != nil {
			log.Printf("ci: set ci status for %s: %v", u.TaskID, err)
			continue
		}
		t, err := p.d.Tasks.Get(u.TaskID)
		if err != nil {
			log.Printf("ci: reload task %s: %v", u.TaskID, err)
			continue
		}
		p.d.Emit("task.updated", t)

		// Spawn a fix task the first time a task's CI transitions to failure.
		if u.Status == "failure" {
			if prev, ok := prevByID[u.TaskID]; !ok || prev.CIStatus != "failure" {
				if err := p.spawnFixTask(t); err != nil {
					log.Printf("ci: spawn fix task for %s: %v", u.TaskID, err)
				}
			}
		}
	}
	return nil
}

// spawnFixTask creates a high-priority fix task for a CI-failed task.
func (p *Poller) spawnFixTask(t *model.Task) error {
	fix := &model.Task{
		Title:    "Fix CI failure: " + t.Title,
		Spec:     fmt.Sprintf("CI failed for task %q.\n\nCI run: %s\n\nOriginal spec:\n%s", t.Title, t.CIRunURL, t.Spec),
		Priority: model.PriorityHigh,
		Reporter: model.ReporterIncident,
	}
	if err := p.d.Tasks.Create(fix); err != nil {
		return err
	}
	p.d.Emit("task.created", fix)
	return nil
}

// Start launches a goroutine that ticks every PollSeconds (minimum 10) calling
// PollOnce. Does nothing when Command is empty.
func (p *Poller) Start(ctx context.Context) {
	if p.d.Command == "" {
		return
	}
	interval := time.Duration(p.d.PollSeconds) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.PollOnce(ctx); err != nil {
					log.Printf("ci: PollOnce returned error: %v", err)
				}
			}
		}
	}()
}
