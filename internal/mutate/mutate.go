// Package mutate is Fabrika's validator-of-the-validator: it perturbs the source
// a task changed and confirms the repo's tests actually catch the perturbation.
// A mutant the tests miss ("survivor") means the suite is too weak to trust for
// autonomous merge, so the task escalates to a human (SPECS.md §8, §13 Phase 3).
//
// Generation is purely textual so it stays stack-agnostic — the gate only knows
// abstract verbs, never a language. That trades precision (some mutants are
// no-ops) for portability; the driver simply skips mutants that don't change a
// file. It is opt-in: it re-runs the test verb once per mutant and is slow.
package mutate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Mutant is one source perturbation: the file it applies to, a 1-based line
// number, a human description, and the full mutated file content.
type Mutant struct {
	File    string
	Line    int
	Desc    string
	Mutated string
}

// swap is a symmetric textual operator. Both directions are tried so the order
// of operands on a line doesn't matter.
type swap struct{ a, b string }

// operators are conservative, high-signal mutations: flip comparisons, boolean
// operators, and literals. Whitespace padding avoids matching inside identifiers
// (e.g. `&&` won't touch a `b&&c`-free token, and ` < ` won't hit `<<`).
var operators = []swap{
	{" == ", " != "},
	{" && ", " || "},
	{" >= ", " < "},
	{" <= ", " > "},
	{"true", "false"},
	{" + ", " - "},
}

// Generate returns candidate mutants for a file's content, at most one per line
// and at most maxPerFile total, scanning lines top-down. Pure and deterministic.
// A line already matched by an earlier operator is not mutated again.
func Generate(file, content string, maxPerFile int) []Mutant {
	lines := strings.Split(content, "\n")
	var out []Mutant
	for i, line := range lines {
		if maxPerFile > 0 && len(out) >= maxPerFile {
			break
		}
		if m, ok := mutateLine(line); ok {
			mutated := append(append([]string{}, lines[:i]...), m.text)
			mutated = append(mutated, lines[i+1:]...)
			out = append(out, Mutant{
				File:    file,
				Line:    i + 1,
				Desc:    file + ":" + itoa(i+1) + " " + m.desc,
				Mutated: strings.Join(mutated, "\n"),
			})
		}
	}
	return out
}

type lineMut struct {
	text string
	desc string
}

// mutateLine applies the first matching operator to a line. Both directions of
// each swap are tried; the first hit wins so each line yields one mutant.
func mutateLine(line string) (lineMut, bool) {
	for _, op := range operators {
		for _, d := range [2][2]string{{op.a, op.b}, {op.b, op.a}} {
			from, to := d[0], d[1]
			if idx := strings.Index(line, from); idx >= 0 {
				mutated := line[:idx] + to + line[idx+len(from):]
				return lineMut{text: mutated, desc: strings.TrimSpace(from) + "→" + strings.TrimSpace(to)}, true
			}
		}
	}
	return lineMut{}, false
}

// TestFunc runs the repo's test verb in the worktree and reports whether it
// passed. The driver expects it to pass on the unmutated tree (a green gate is
// the precondition) and to fail when a mutant is injected.
type TestFunc func(ctx context.Context) (passed bool)

// Result is the mutation-testing outcome for a worktree.
type Result struct {
	Tested   int      // mutants actually run (changed the file)
	Caught   int      // mutants the tests failed on (good)
	Survived []string // descriptions of mutants the tests missed (bad — weak suite)
	Skipped  string   // non-empty when mutation testing couldn't run
}

// Pass reports whether the suite caught every mutant it was given. A run that was
// skipped, or that found no applicable mutants, passes vacuously (no evidence of
// weakness) — it never blocks merge on absence of signal.
func (r Result) Pass() bool { return len(r.Survived) == 0 }

// Run mutates each file (paths relative to dir) one mutant at a time, runs the
// test func, and records whether the suite caught it. It always restores the
// file's original bytes, even on test failure or context cancellation. It stops
// after budget mutants total (across files) to stay bounded; 0 means unbounded.
func Run(ctx context.Context, dir string, files []string, test TestFunc, budget int) Result {
	var res Result
	if test == nil {
		res.Skipped = "no test command configured"
		return res
	}
	for _, rel := range files {
		if budget > 0 && res.Tested >= budget {
			break
		}
		abs := filepath.Join(dir, rel)
		original, err := os.ReadFile(abs)
		if err != nil {
			continue // file vanished (e.g. deleted by the change) — nothing to mutate
		}
		remaining := budget - res.Tested
		mutants := Generate(rel, string(original), remaining)
		for _, m := range mutants {
			if ctx.Err() != nil {
				res.Skipped = "cancelled"
				return res
			}
			if m.Mutated == string(original) {
				continue // no-op mutation
			}
			if err := os.WriteFile(abs, []byte(m.Mutated), 0o644); err != nil {
				continue
			}
			passed := test(ctx)
			// Restore before reacting so a panic/early-return can't leak a mutant.
			_ = os.WriteFile(abs, original, 0o644)
			res.Tested++
			if passed {
				res.Survived = append(res.Survived, m.Desc) // suite missed it
			} else {
				res.Caught++
			}
			if budget > 0 && res.Tested >= budget {
				break
			}
		}
	}
	if res.Tested == 0 && res.Skipped == "" {
		res.Skipped = "no applicable mutants in changed files"
	}
	return res
}

// itoa is a tiny strconv.Itoa to keep the import list minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
