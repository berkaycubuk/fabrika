// Package feedback implements the production-signal poller: it polls one or more
// sources (command, sentry-stub) for error events, deduplicates them by
// fingerprint, and spawns fix tasks for new or reopened incidents.
// See SPECS-PHASE4 §5.6.
package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Commander runs a command in a workdir, returning combined output and an error
// on non-zero exit. Satisfied by gate.CommandRunner and the engine's engineCommander.
type Commander interface {
	RunCommand(ctx context.Context, workdir, command string, env []string) (string, error)
}

// Event is a single production error event received from a source.
// Extra source fields not mapped to struct fields are preserved in Payload as
// raw JSON.
type Event struct {
	Title       string `json:"title"`
	Stack       string `json:"stack"`
	Fingerprint string `json:"fingerprint"`
	Payload     string `json:"-"`
}

// Source is anything that can return a batch of Events.
type Source interface {
	Poll(ctx context.Context) ([]Event, error)
}

// Deps is the dependency bundle passed to NewManager.
type Deps struct {
	Incidents *store.IncidentRepo
	Tasks     *store.TaskRepo
	Sources   []config.FeedbackSource
	RepoRoot  string
	Cmd       Commander
	Emit      func(string, any)
	Now       func() time.Time
}

// Manager polls feedback sources and ingests events into incidents and tasks.
type Manager struct {
	d Deps
}

// NewManager constructs a Manager. A nil Emit is replaced with a no-op; a nil
// Now is replaced with time.Now.
func NewManager(d Deps) *Manager {
	if d.Emit == nil {
		d.Emit = func(string, any) {}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Manager{d: d}
}

// fileLineRE matches file:line pairs in stack traces (e.g. handler.go:42).
var fileLineRE = regexp.MustCompile(`[^\s/\\]+\.go:\d+`)

// Fingerprint returns e.Fingerprint when non-empty, otherwise the first 16 hex
// characters of sha256(title + first 5 stack file:line pairs).
func Fingerprint(e Event) string {
	if e.Fingerprint != "" {
		return e.Fingerprint
	}
	pairs := fileLineRE.FindAllString(e.Stack, -1)
	if len(pairs) > 5 {
		pairs = pairs[:5]
	}
	h := sha256.Sum256([]byte(e.Title + strings.Join(pairs, "")))
	return fmt.Sprintf("%x", h)[:16]
}

// commandSource is a Source backed by a shell command whose stdout is a JSON
// array of event objects.
type commandSource struct {
	cmd      Commander
	workdir  string
	command  string
}

func (s *commandSource) Poll(ctx context.Context) ([]Event, error) {
	out, err := s.cmd.RunCommand(ctx, s.workdir, s.command, nil)
	if err != nil {
		return nil, err
	}
	return parseEvents(out)
}

// parseEvents decodes a JSON array of raw objects into Events, preserving the
// full raw object in Event.Payload.
func parseEvents(data string) ([]Event, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal([]byte(data), &raws); err != nil {
		return nil, fmt.Errorf("parse events: %w", err)
	}
	events := make([]Event, 0, len(raws))
	for _, raw := range raws {
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("parse event: %w", err)
		}
		e.Payload = string(raw)
		events = append(events, e)
	}
	return events, nil
}

// sentrySource is a stub; Sentry integration is not yet implemented.
type sentrySource struct{}

func (s *sentrySource) Poll(_ context.Context) ([]Event, error) { return nil, nil }

// PollOnce runs one tick for the given source: polls it, then ingests each event.
// It never panics: any panic, non-zero command exit, or malformed JSON is logged
// and the tick is skipped with a nil return.
func (m *Manager) PollOnce(ctx context.Context, src config.FeedbackSource) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("feedback: PollOnce panic (type=%s): %v", src.Type, r)
			retErr = nil
		}
	}()

	var source Source
	switch src.Type {
	case "command":
		source = &commandSource{cmd: m.d.Cmd, workdir: m.d.RepoRoot, command: src.Command}
	case "sentry":
		source = &sentrySource{}
	default:
		log.Printf("feedback: unknown source type %q, skipping", src.Type)
		return nil
	}

	events, err := source.Poll(ctx)
	if err != nil {
		log.Printf("feedback: poll error (type=%s): %v", src.Type, err)
		return nil
	}

	for _, ev := range events {
		if err := m.ingest(ctx, ev); err != nil {
			log.Printf("feedback: ingest error: %v", err)
		}
	}
	return nil
}

// ingest deduplicates and persists one event, spawning a fix task as needed.
func (m *Manager) ingest(_ context.Context, ev Event) error {
	fp := Fingerprint(ev)
	now := m.d.Now().UTC().Format("2006-01-02 15:04:05")

	existing, err := m.d.Incidents.GetByFingerprint(fp)
	if errors.Is(err, store.ErrNotFound) {
		// New incident: create and immediately spawn a fix task.
		inc := &model.Incident{
			Fingerprint: fp,
			Title:       ev.Title,
			Stack:       ev.Stack,
			Payload:     ev.Payload,
			Count:       1,
			FirstSeen:   now,
			LastSeen:    now,
			Status:      model.IncidentOpen,
		}
		if err := m.d.Incidents.Create(inc); err != nil {
			return fmt.Errorf("create incident: %w", err)
		}
		m.d.Emit("incident.created", *inc)

		task, err := m.spawnFixTask(inc)
		if err != nil {
			return fmt.Errorf("spawn fix task: %w", err)
		}
		inc.TaskID = task.ID
		inc.Status = model.IncidentFixing
		if err := m.d.Incidents.Update(inc); err != nil {
			return fmt.Errorf("update incident after task spawn: %w", err)
		}
		m.d.Emit("task.created", *task)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get incident by fingerprint: %w", err)
	}

	// Existing incident.
	existing.Count++
	existing.LastSeen = now

	if existing.Status == model.IncidentResolved {
		// Reopen: spawn a fresh fix task.
		existing.Status = model.IncidentFixing
		if err := m.d.Incidents.Update(existing); err != nil {
			return fmt.Errorf("reopen incident: %w", err)
		}
		m.d.Emit("incident.updated", *existing)

		task, err := m.spawnFixTask(existing)
		if err != nil {
			return fmt.Errorf("spawn fix task on reopen: %w", err)
		}
		existing.TaskID = task.ID
		if err := m.d.Incidents.Update(existing); err != nil {
			return fmt.Errorf("update incident task_id on reopen: %w", err)
		}
		m.d.Emit("task.created", *task)
		return nil
	}

	// open, fixing, or ignored: bump counters only.
	if err := m.d.Incidents.Update(existing); err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	m.d.Emit("incident.updated", *existing)
	return nil
}

// spawnFixTask creates and persists a high-priority fix task for an incident.
func (m *Manager) spawnFixTask(inc *model.Incident) (*model.Task, error) {
	spec := fmt.Sprintf("%s\n\nOccurrences: %d\nFirst seen: %s\nLast seen: %s",
		inc.Stack, inc.Count, inc.FirstSeen, inc.LastSeen)
	task := &model.Task{
		Title:    "Fix incident: " + inc.Title,
		Spec:     spec,
		Priority: model.PriorityHigh,
		Reporter: model.ReporterIncident,
	}
	if err := m.d.Tasks.Create(task); err != nil {
		return nil, err
	}
	return task, nil
}

// Start launches one goroutine per source, each ticking every poll_seconds and
// calling PollOnce. All goroutines stop when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	for _, src := range m.d.Sources {
		go func() {
			interval := time.Duration(src.PollSeconds) * time.Second
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := m.PollOnce(ctx, src); err != nil {
						log.Printf("feedback: PollOnce returned error: %v", err)
					}
				}
			}
		}()
	}
}
