package engine

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/schedule"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// cronTask builds the Task that a fired schedule enqueues.
func cronTask(c model.CronSchedule) model.Task {
	title := c.Title
	if title == "" {
		title = "Scheduled: " + c.Expr
	}
	return model.Task{
		Title:            title,
		Spec:             c.Prompt,
		PreferredAgentID: c.AgentID,
		Reporter:         model.ReporterCron,
		Status:           model.TaskReady,
	}
}

// FireCron looks up a schedule by id, enqueues a task for it, advances the
// next-run timestamp, emits UI events, and wakes the dispatch loop.
func (e *Engine) FireCron(id string) (*model.Task, error) {
	c, err := e.store.Crons.Get(id)
	if err != nil {
		return nil, err // propagates store.ErrNotFound
	}

	t := cronTask(*c)
	if err := e.store.Tasks.Create(&t); err != nil {
		return nil, err
	}

	now := time.Now()
	nowStr := now.UTC().Format(time.RFC3339)
	nextStr := c.NextRunAt // leave unchanged on parse error
	if next, nerr := schedule.Next(c.Expr, now); nerr != nil {
		log.Printf("engine: cron %q: compute next run: %v", id, nerr)
	} else {
		nextStr = next.UTC().Format(time.RFC3339)
	}

	if err := e.store.Crons.MarkRun(id, nowStr, nextStr); err != nil {
		log.Printf("engine: cron %q: mark run: %v", id, err)
	}

	e.emit("cron.fired", map[string]string{"id": id})
	e.emit("task.created", t)
	e.Wake()
	return &t, nil
}

// cronLoop fires due schedules on a 30-second ticker until ctx is cancelled.
func (e *Engine) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			e.fireDueCrons(now)
		}
	}
}

// fireDueCrons fires every enabled schedule whose NextRunAt is at or before now.
func (e *Engine) fireDueCrons(now time.Time) {
	list, err := e.store.Crons.List()
	if err != nil {
		log.Printf("engine: cron: list schedules: %v", err)
		return
	}
	for _, c := range list {
		if !schedule.IsDue(c.NextRunAt, c.Enabled, now) {
			continue
		}
		if _, err := e.FireCron(c.ID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			log.Printf("engine: cron: fire %q: %v", c.ID, err)
		}
	}
}
