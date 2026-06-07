// Package release implements the single-flight release Manager: Ship deploys
// the current HEAD through a configured command, tracks bake windows, and
// Rollback reverts to the previous SHA. The Manager serializes Ship/Rollback
// with an internal sync.Mutex.
package release

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// TimeLayout is the UTC format used to stamp and parse deployed_at and live_at.
// Matches SQLite's datetime('now') format.
const TimeLayout = "2006-01-02 15:04:05"

// Commander runs a command in a workdir, returning combined output and an error
// on non-zero exit.
type Commander interface {
	RunCommand(ctx context.Context, workdir, command string, env []string) (string, error)
}

// Gitter exposes the git operations the Manager needs.
type Gitter interface {
	RevParse(ctx context.Context, ref string) (string, error)
	RevList(ctx context.Context, rng string) ([]string, error)
	AddWorktreeDetached(ctx context.Context, path, ref string) error
	RemoveWorktree(ctx context.Context, path string) error
}

// Deps is the dependency bundle passed to NewManager.
type Deps struct {
	Releases *store.ReleaseRepo
	Tasks    *store.TaskRepo
	Deploy   config.Deploy
	RepoRoot string
	Cmd      Commander
	Git      Gitter
	Emit     func(string, any)
	Now      func() time.Time
}

// Manager serializes Ship and Rollback with an internal mutex. Cmd and Git may
// be nil at construction time; only Ship and Rollback actually use them.
type Manager struct {
	mu sync.Mutex
	d  Deps
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

// Ship deploys the current HEAD under the mutex (single-flight). It returns a
// non-nil error only for guard failures (disabled, in-motion, nothing to ship)
// and infra failures (RevParse, Create). Deploy/health command failures set the
// release status to 'failed' and return (release, nil) so the API can surface
// the failure without treating it as an internal error.
func (m *Manager) Ship(ctx context.Context) (*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// (a) deploy disabled
	if m.d.Deploy.Command == "" {
		return nil, fmt.Errorf("deploy disabled: no command configured")
	}

	// (b) single-flight: block if any release is already in motion
	if _, err := m.d.Releases.InMotion(); err == nil {
		return nil, fmt.Errorf("deploy conflict: a release is already in motion")
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("deploy: check in-motion: %w", err)
	}

	// (c) nothing to ship
	unshipped, err := m.unshipped()
	if err != nil {
		return nil, fmt.Errorf("deploy: list unshipped tasks: %w", err)
	}
	if len(unshipped) == 0 {
		return nil, fmt.Errorf("deploy: nothing to ship")
	}

	// resolve the current HEAD SHA (infra error -> non-nil return)
	sha, err := m.d.Git.RevParse(ctx, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("deploy: rev-parse HEAD: %w", err)
	}

	// find the previously-deployed SHA for rollback
	prevSHA := ""
	if prev, err := m.d.Releases.LatestDeployed(); err == nil {
		prevSHA = prev.SHA
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("deploy: latest deployed: %w", err)
	}

	// create the release row in deploying state
	rel := &model.Release{
		SHA:     sha,
		PrevSHA: prevSHA,
		Status:  model.ReleaseDeploying,
	}
	if err := m.d.Releases.Create(rel); err != nil {
		return nil, fmt.Errorf("deploy: create release: %w", err)
	}

	// run the deploy command
	out, deployErr := m.d.Cmd.RunCommand(ctx, m.d.RepoRoot, m.d.Deploy.Command, nil)
	rel.DeployLog += out
	if deployErr != nil {
		rel.Status = model.ReleaseFailed
		rel.Error = deployErr.Error()
		_ = m.d.Releases.Update(rel)
		m.d.Emit("release.updated", *rel)
		if prevSHA != "" {
			_ = m.rollbackTo(ctx, prevSHA)
		}
		return rel, nil
	}
	_ = m.d.Releases.Update(rel)

	// optional health check
	if m.d.Deploy.Health != "" {
		hout, healthErr := m.d.Cmd.RunCommand(ctx, m.d.RepoRoot, m.d.Deploy.Health, nil)
		rel.HealthLog += hout
		if healthErr != nil {
			rel.Status = model.ReleaseFailed
			rel.Error = healthErr.Error()
			_ = m.d.Releases.Update(rel)
			m.d.Emit("release.updated", *rel)
			if prevSHA != "" {
				_ = m.rollbackTo(ctx, prevSHA)
			}
			return rel, nil
		}
		_ = m.d.Releases.Update(rel)
	}

	// coverage: stamp tasks whose merge commit falls in this release's range
	rng := sha
	if prevSHA != "" {
		rng = prevSHA + ".." + sha
	}
	if shas, err := m.d.Git.RevList(ctx, rng); err == nil {
		shaSet := make(map[string]bool, len(shas))
		for _, s := range shas {
			shaSet[s] = true
		}
		if tasks, err := m.d.Tasks.List(); err == nil {
			for _, t := range tasks {
				if t.Status == model.TaskMerged && t.MergeCommitSHA != "" && shaSet[t.MergeCommitSHA] {
					_ = m.d.Tasks.SetReleaseID(t.ID, rel.ID)
				}
			}
		}
	}

	// stamp deployed_at and transition to live or baking
	rel.DeployedAt = m.d.Now().UTC().Format(TimeLayout)

	if m.d.Deploy.BakeMinutes == 0 {
		rel.Status = model.ReleaseLive
		rel.LiveAt = m.d.Now().UTC().Format(TimeLayout)
		_ = m.d.Releases.Update(rel)
		m.d.Emit("release.updated", *rel)
	} else {
		rel.Status = model.ReleaseBaking
		_ = m.d.Releases.Update(rel)
		m.d.Emit("release.updated", *rel)
		// IMPORTANT: Ship holds m.mu here. startBake must NOT acquire m.mu in its
		// own body — only the goroutine it spawns may, and only after Ship returns.
		m.startBake(rel)
	}

	return rel, nil
}

// Rollback reverts a baking or live release by running the rollback command (or
// re-deploying at the previous SHA). Covered tasks keep their release_id so the
// history is preserved; they re-appear in Unshipped because the release is now
// rolled_back.
func (m *Manager) Rollback(ctx context.Context, releaseID string) (*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rel, err := m.d.Releases.Get(releaseID)
	if err != nil {
		return nil, err
	}
	if rel.Status != model.ReleaseBaking && rel.Status != model.ReleaseLive {
		return nil, fmt.Errorf("rollback: release is %s, not baking or live", rel.Status)
	}

	_ = m.rollbackTo(ctx, rel.PrevSHA)

	rel.Status = model.ReleaseRolledBack
	if err := m.d.Releases.Update(rel); err != nil {
		return nil, err
	}
	m.d.Emit("release.updated", *rel)
	return rel, nil
}

// rollbackTo runs the configured rollback command, or creates a temporary
// worktree at prevSHA and re-runs the deploy command there. prevSHA=="" is a
// no-op (first release). The caller must hold m.mu.
func (m *Manager) rollbackTo(ctx context.Context, prevSHA string) error {
	if prevSHA == "" {
		return nil
	}
	if m.d.Deploy.Rollback != "" {
		_, err := m.d.Cmd.RunCommand(ctx, m.d.RepoRoot, m.d.Deploy.Rollback,
			[]string{"FABRIKA_ROLLBACK_SHA=" + prevSHA})
		return err
	}
	// Fallback: create a detached worktree at prevSHA, run deploy there, then remove.
	wtPath := filepath.Join(m.d.RepoRoot, ".fabrika", "rollback-"+prevSHA)
	if err := m.d.Git.AddWorktreeDetached(ctx, wtPath, prevSHA); err != nil {
		return err
	}
	_, runErr := m.d.Cmd.RunCommand(ctx, wtPath, m.d.Deploy.Command, nil)
	_ = m.d.Git.RemoveWorktree(ctx, wtPath)
	return runErr
}

// Unshipped returns merged tasks not covered by an active release. A task is
// "unshipped" if its release_id is empty or points to a failed/rolled_back
// release. Does not acquire m.mu (read-only; safe to call concurrently and
// from within Ship under the lock).
func (m *Manager) Unshipped() ([]model.Task, error) {
	return m.unshipped()
}

// unshipped is the internal implementation. May be called while m.mu is held.
func (m *Manager) unshipped() ([]model.Task, error) {
	releases, err := m.d.Releases.List()
	if err != nil {
		return nil, err
	}

	// build release_id -> status index
	releaseStatus := make(map[string]string, len(releases))
	for _, r := range releases {
		releaseStatus[r.ID] = r.Status
	}

	tasks, err := m.d.Tasks.List()
	if err != nil {
		return nil, err
	}

	var out []model.Task
	for _, t := range tasks {
		if t.Status != model.TaskMerged {
			continue
		}
		if t.ReleaseID == "" {
			out = append(out, t)
			continue
		}
		s := releaseStatus[t.ReleaseID]
		if s == model.ReleaseFailed || s == model.ReleaseRolledBack {
			out = append(out, t)
		}
	}
	return out, nil
}

// ResumeBakeTimers is called on startup to restore bake timers that survived a
// process restart. For each baking release, if the bake window has already
// elapsed the transition to live happens synchronously; otherwise a background
// goroutine is started for the remaining duration. State is always derived from
// deployed_at + BakeMinutes (never persisted separately).
func (m *Manager) ResumeBakeTimers() {
	releases, err := m.d.Releases.List()
	if err != nil {
		return
	}
	bakeD := time.Duration(m.d.Deploy.BakeMinutes) * time.Minute
	for _, rel := range releases {
		if rel.Status != model.ReleaseBaking {
			continue
		}
		deployedAt, err := time.Parse(TimeLayout, rel.DeployedAt)
		if err != nil {
			continue
		}
		elapsed := m.d.Now().Sub(deployedAt)
		if elapsed >= bakeD {
			// Bake window already passed — transition synchronously.
			relCopy := rel
			relCopy.Status = model.ReleaseLive
			relCopy.LiveAt = m.d.Now().UTC().Format(TimeLayout)
			_ = m.d.Releases.Update(&relCopy)
			m.d.Emit("release.updated", relCopy)
		} else {
			// Start a goroutine for the remaining bake window.
			remaining := bakeD - elapsed
			relCopy := rel
			go func() {
				time.Sleep(remaining)
				m.mu.Lock()
				defer m.mu.Unlock()
				current, err := m.d.Releases.Get(relCopy.ID)
				if err != nil || current.Status != model.ReleaseBaking {
					return
				}
				current.Status = model.ReleaseLive
				current.LiveAt = m.d.Now().UTC().Format(TimeLayout)
				_ = m.d.Releases.Update(current)
				m.d.Emit("release.updated", *current)
			}()
		}
	}
}

// startBake starts the background bake timer for rel.
//
// CRITICAL: this is called from Ship while m.mu is held. This method must NOT
// acquire m.mu in its own body. Only the goroutine it spawns may acquire m.mu,
// and only after Ship returns (releasing the lock). Acquiring the lock here
// would deadlock because Ship already holds it.
func (m *Manager) startBake(rel *model.Release) {
	bakeD := time.Duration(m.d.Deploy.BakeMinutes) * time.Minute
	go func() {
		time.Sleep(bakeD)
		// Now safe to acquire the lock — Ship has long since returned.
		m.mu.Lock()
		defer m.mu.Unlock()
		current, err := m.d.Releases.Get(rel.ID)
		if err != nil || current.Status != model.ReleaseBaking {
			// Already transitioned (e.g. rolled back) — no-op.
			return
		}
		current.Status = model.ReleaseLive
		current.LiveAt = m.d.Now().UTC().Format(TimeLayout)
		_ = m.d.Releases.Update(current)
		m.d.Emit("release.updated", *current)
	}()
}
