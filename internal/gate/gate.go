// Package gate runs a repo's verification verbs against a worktree and
// normalizes the outcome into a model.Evidence. Stages run in a fixed order and
// stop on the first hard failure. Missing verbs are skipped, not failed.
//
// This is plumbing for the deferred live loop: Run is usable and tested, but no
// scheduler invokes it yet. Integrity rules (locked globs, held-out checks,
// mutation testing) arrive in Phase 2+. See SPECS.md §8.
package gate

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
)

// stageOrder is the fixed execution order of gate stages (SPECS §8).
var stageOrder = []string{"setup", "typecheck", "lint", "build", "test", "verify", "e2e"}

// Runner executes the verification gate for a task.
type Runner interface {
	Run(ctx context.Context, workdir string, verbs config.Verbs, verifyCmds []string) (model.Evidence, error)
}

// CommandRunner is the default Runner: it executes each verb as a shell command
// in workdir. It satisfies Runner.
type CommandRunner struct {
	// Shell is the shell used to run verb command strings. Defaults to "sh -c".
	Shell []string
}

// New returns a CommandRunner with sensible defaults.
func New() *CommandRunner {
	return &CommandRunner{Shell: []string{"sh", "-c"}}
}

// Run executes the gate stages in order against workdir. The map of verbs comes
// from the repo manifest; verifyCmds are the task's spec-derived acceptance
// commands and run as part of the "verify" stage. Execution stops at the first
// failing stage; later stages are recorded as skipped.
func (r *CommandRunner) Run(ctx context.Context, workdir string, verbs config.Verbs, verifyCmds []string) (model.Evidence, error) {
	ev := model.Evidence{Stages: map[string]model.StageResult{}}

	verbCmd := map[string]string{
		"setup":     verbs.Setup,
		"typecheck": verbs.Typecheck,
		"lint":      verbs.Lint,
		"build":     verbs.Build,
		"test":      verbs.Test,
		"verify":    verbs.Verify,
		"e2e":       verbs.E2E,
	}

	failed := false
	for _, stage := range stageOrder {
		// Collect the commands this stage runs.
		cmds := []string{}
		if c := verbCmd[stage]; c != "" {
			cmds = append(cmds, c)
		}
		if stage == "verify" {
			cmds = append(cmds, verifyCmds...)
		}

		// No commands, or a prior stage already failed -> skipped.
		if len(cmds) == 0 || failed {
			ev.Stages[stage] = model.StageResult{Skipped: true, Pass: true}
			continue
		}

		res := r.runStage(ctx, workdir, cmds)
		ev.Stages[stage] = res
		if !res.Pass {
			failed = true
		}
	}
	return ev, nil
}

// runStage runs each command in sequence; the stage fails on the first non-zero
// exit. Output is concatenated.
func (r *CommandRunner) runStage(ctx context.Context, workdir string, cmds []string) model.StageResult {
	var out bytes.Buffer
	shell := r.Shell
	if len(shell) == 0 {
		shell = []string{"sh", "-c"}
	}
	for _, c := range cmds {
		args := append(append([]string{}, shell[1:]...), c)
		cmd := exec.CommandContext(ctx, shell[0], args...)
		cmd.Dir = workdir
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			exit := 1
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			}
			return model.StageResult{Pass: false, Output: out.String(), ExitCode: exit}
		}
	}
	return model.StageResult{Pass: true, Output: out.String()}
}
