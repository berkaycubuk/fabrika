package engine

import (
	"context"

	"github.com/berkaycubuk/fabrika/internal/gate"
	"github.com/berkaycubuk/fabrika/internal/git"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// ReleaseDetail bundles a release with its covered tasks.
type ReleaseDetail struct {
	Release model.Release `json:"release"`
	Tasks   []model.Task  `json:"tasks"`
}

// Ship triggers a deployment of the current HEAD via the release Manager.
func (e *Engine) Ship(ctx context.Context) (*model.Release, error) {
	return e.release.Ship(ctx)
}

// Rollback reverts a baking or live release to its previous SHA.
func (e *Engine) Rollback(ctx context.Context, id string) (*model.Release, error) {
	return e.release.Rollback(ctx, id)
}

// ListReleases returns all releases newest-first (delegates to store).
func (e *Engine) ListReleases() ([]model.Release, error) {
	return e.store.Releases.List()
}

// UnshippedTasks returns merged tasks not covered by an active release.
func (e *Engine) UnshippedTasks() ([]model.Task, error) {
	return e.release.Unshipped()
}

// GetRelease returns a release and the tasks it covers (release_id == id).
func (e *Engine) GetRelease(id string) (ReleaseDetail, error) {
	rel, err := e.store.Releases.Get(id)
	if err != nil {
		return ReleaseDetail{}, err
	}
	tasks, err := e.store.Tasks.List()
	if err != nil {
		return ReleaseDetail{}, err
	}
	var covered []model.Task
	for _, t := range tasks {
		if t.ReleaseID == id {
			covered = append(covered, t)
		}
	}
	return ReleaseDetail{Release: *rel, Tasks: covered}, nil
}

// engineCommander implements release.Commander by delegating to gate.New().RunCommand.
// Each call constructs a fresh CommandRunner so there is no shared state.
type engineCommander struct{}

func (engineCommander) RunCommand(ctx context.Context, workdir, command string, env []string) (string, error) {
	return gate.New().RunCommand(ctx, workdir, command, env)
}

// engineGitter implements release.Gitter by opening a git.Repo per call.
type engineGitter struct {
	repoRoot string
}

func (g engineGitter) RevParse(ctx context.Context, ref string) (string, error) {
	repo, err := git.Open(ctx, g.repoRoot)
	if err != nil {
		return "", err
	}
	return repo.RevParse(ctx, ref)
}

func (g engineGitter) RevList(ctx context.Context, rng string) ([]string, error) {
	repo, err := git.Open(ctx, g.repoRoot)
	if err != nil {
		return nil, err
	}
	return repo.RevList(ctx, rng)
}

func (g engineGitter) AddWorktreeDetached(ctx context.Context, path, ref string) error {
	repo, err := git.Open(ctx, g.repoRoot)
	if err != nil {
		return err
	}
	return repo.AddWorktreeDetached(ctx, path, ref)
}

func (g engineGitter) RemoveWorktree(ctx context.Context, path string) error {
	repo, err := git.Open(ctx, g.repoRoot)
	if err != nil {
		return err
	}
	return repo.RemoveWorktree(ctx, path)
}
