package planner

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// This file enforces a contract invariant the planner prompt can only ask for:
// "Never reference a held-out file you did not provide." A held-out command that
// references a file which neither exists in the repo, is authored in
// heldOutFiles, nor will be created by the task itself (touchPaths) can NEVER
// pass — the implementer is locked out of held-out paths, so the task is doomed
// before dispatch and fails only after a full (correct) agent run. Validating
// here turns that late, misattributed failure into an immediate plan rejection.

// ValidateHeldOut checks every task in a raw plan and returns one human/planner
// actionable message per unsatisfiable held-out file reference. An empty result
// means the plan's held-out checks are runnable as authored.
func ValidateHeldOut(root string, raw RawPlan) []string {
	var issues []string
	for _, rt := range raw.Tasks {
		for _, miss := range MissingHeldOutRefs(root, rt.Acceptance, rt.TouchPaths) {
			issues = append(issues, fmt.Sprintf(
				"task %q: held-out check references %q, which does not exist in the repo and is not authored in heldOutFiles — author the full file in heldOutFiles or reference an existing file",
				rt.Title, miss))
		}
	}
	return issues
}

// MissingHeldOutRefs returns the file paths referenced by a contract's held-out
// commands that cannot be satisfied: they do not exist under root, are not
// authored in HeldOutFiles, and are not covered by touchPaths (files the
// implementer is expected to create). Returned paths are root-relative.
func MissingHeldOutRefs(root string, c model.Contract, touchPaths []string) []string {
	var missing []string
	seen := map[string]bool{}
	for _, cmd := range c.HeldOut {
		for _, ref := range commandFileRefs(cmd) {
			if seen[ref] || refSatisfied(root, ref, c.HeldOutFiles, touchPaths) {
				continue
			}
			seen[ref] = true
			missing = append(missing, ref)
		}
	}
	return missing
}

// fileToken matches a shell token that plausibly names a file: word/path
// characters ending in a letter-led extension (so `./...`, `v1.2`, and bare
// command names never match).
var fileToken = regexp.MustCompile(`^[\w@./-]+\.[A-Za-z][A-Za-z0-9]*$`)

// commandFileRefs extracts the file paths a shell command references, resolved
// relative to the worktree root. It tracks `cd <dir>` across segments so
// `cd web && node --test test/x.test.ts` yields `web/test/x.test.ts`. Tokens
// that are flags, globs, URLs, or contain shell variables are skipped; if the
// command cd's somewhere unresolvable (absolute, `..`, `$VAR`), the rest of
// that command is skipped rather than risk a false positive.
func commandFileRefs(cmd string) []string {
	var refs []string
	dir := "."
	for _, seg := range splitCommand(cmd) {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "cd" {
			if len(fields) < 2 {
				continue
			}
			d := strings.Trim(fields[1], `"'`)
			if d == "" || strings.HasPrefix(d, "/") || strings.ContainsAny(d, "$~*") ||
				d == ".." || strings.HasPrefix(d, "../") {
				return refs // can't resolve subsequent relative paths confidently
			}
			dir = path.Join(dir, d)
			continue
		}
		for i, f := range fields {
			// Skip output-redirection targets (`> out.log`): they are created
			// by the command, not required to pre-exist.
			if i > 0 && strings.HasSuffix(fields[i-1], ">") {
				continue
			}
			f = strings.Trim(f, `"'`)
			if f == "" || strings.HasPrefix(f, "-") || strings.ContainsAny(f, "$*?[<>") ||
				strings.Contains(f, "://") {
				continue
			}
			if !fileToken.MatchString(f) {
				continue
			}
			refs = append(refs, path.Join(dir, strings.TrimPrefix(f, "./")))
		}
	}
	return refs
}

// splitCommand breaks a shell command into segments at &&, ||, ; and |.
func splitCommand(cmd string) []string {
	return strings.FieldsFunc(strings.NewReplacer("&&", ";", "||", ";", "|", ";").Replace(cmd),
		func(r rune) bool { return r == ';' })
}

// refSatisfied reports whether a root-relative file reference is runnable at
// gate time: it already exists, the planner authored it in heldOutFiles, or the
// implementer is expected to create it (covered by touchPaths).
func refSatisfied(root, ref string, heldOutFiles map[string]string, touchPaths []string) bool {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ref))); err == nil {
		return true
	}
	for p := range heldOutFiles {
		if path.Clean(strings.TrimPrefix(p, "./")) == ref {
			return true
		}
	}
	for _, tp := range touchPaths {
		tp = strings.TrimSuffix(path.Clean(strings.TrimPrefix(tp, "./")), "/")
		if tp == "" || tp == "." {
			continue
		}
		if ref == tp || strings.HasPrefix(ref, tp+"/") {
			return true
		}
	}
	return false
}
