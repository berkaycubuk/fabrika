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
	"fmt"
	"os"
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
	// Shell is the shell used to run verb command strings. Defaults to "bash -c".
	Shell []string
}

// New returns a CommandRunner with sensible defaults.
func New() *CommandRunner {
	return &CommandRunner{Shell: []string{"bash", "-c"}}
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

// RunCommand runs a single command through the configured shell in workdir,
// capturing combined stdout+stderr. env entries are appended to os.Environ() on
// the child process. On non-zero exit the output is still returned alongside a
// non-nil error.
func (r *CommandRunner) RunCommand(ctx context.Context, workdir, command string, env []string) (string, error) {
	out, err := r.runShell(ctx, workdir, command, env)
	if err != nil {
		return out, fmt.Errorf("command %q: %w", command, err)
	}
	return out, nil
}

// runShell executes a single command through the configured shell in workdir.
// env is appended to os.Environ() when non-empty. Returns combined stdout+stderr
// and the raw error from cmd.Run.
func (r *CommandRunner) runShell(ctx context.Context, workdir, command string, env []string) (string, error) {
	shell := r.Shell
	if len(shell) == 0 {
		shell = []string{"bash", "-c"}
	}
	args := append(append([]string{}, shell[1:]...), command)
	cmd := exec.CommandContext(ctx, shell[0], args...)
	cmd.Dir = workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	err := cmd.Run()
	return out.String(), err
}

// runStage runs each command in sequence; the stage fails on the first non-zero
// exit. Output is concatenated.
func (r *CommandRunner) runStage(ctx context.Context, workdir string, cmds []string) model.StageResult {
	var out bytes.Buffer
	for _, c := range cmds {
		output, err := r.runShell(ctx, workdir, c, nil)
		out.WriteString(output)
		if err != nil {
			exit := 1
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			}
			return model.StageResult{Pass: false, Output: out.String(), ExitCode: exit}
		}
	}
	return model.StageResult{Pass: true, Output: out.String()}
}
