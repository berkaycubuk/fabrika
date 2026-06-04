package planner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

// repoWith creates a temp repo root containing the given (slash-separated)
// relative files.
func repoWith(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestMissingHeldOutRefs(t *testing.T) {
	cases := []struct {
		name       string
		files      []string
		contract   model.Contract
		touchPaths []string
		want       []string
	}{
		{
			name: "missing held-out test file is reported (the renderDiff failure)",
			contract: model.Contract{
				HeldOut: []string{"cd web && npm install --silent && node --import tsx --test test/heldout/renderDiff.heldout.test.ts"},
			},
			want: []string{"web/test/heldout/renderDiff.heldout.test.ts"},
		},
		{
			name:  "existing repo file passes",
			files: []string{"web/test/heldout/x.heldout.test.ts"},
			contract: model.Contract{
				HeldOut: []string{"cd web && node --import tsx --test test/heldout/x.heldout.test.ts"},
			},
		},
		{
			name: "file authored in heldOutFiles passes",
			contract: model.Contract{
				HeldOut:      []string{"cd web && node --import tsx --test test/heldout/x.heldout.test.ts"},
				HeldOutFiles: map[string]string{"web/test/heldout/x.heldout.test.ts": "contents"},
			},
		},
		{
			name: "file covered by touchPaths passes (implementer will create it)",
			contract: model.Contract{
				HeldOut: []string{"npx tsc --noEmit web/src/views/diff-view.ts"},
			},
			touchPaths: []string{"web/src/views/diff-view.ts"},
		},
		{
			name: "touchPaths directory prefix covers nested refs",
			contract: model.Contract{
				HeldOut: []string{"go test internal/foo/hidden_test.go"},
			},
			touchPaths: []string{"internal/foo"},
		},
		{
			name: "flags globs package-paths and redirect targets are not refs",
			contract: model.Contract{
				HeldOut: []string{"go test -run Hidden ./... > out.log 2>&1", `node --test "test/*.test.ts"`},
			},
		},
		{
			name: "unresolvable cd stops checking instead of guessing",
			contract: model.Contract{
				HeldOut: []string{"cd $TMPDIR && node missing.test.ts"},
			},
		},
		{
			name: "sequential cd accumulates",
			contract: model.Contract{
				HeldOut: []string{"cd web; cd test && node x.test.ts"},
			},
			want: []string{"web/test/x.test.ts"},
		},
		{
			name: "duplicate refs reported once",
			contract: model.Contract{
				HeldOut: []string{"node a/x.test.ts", "node a/x.test.ts"},
			},
			want: []string{"a/x.test.ts"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := repoWith(t, tc.files...)
			got := MissingHeldOutRefs(root, tc.contract, tc.touchPaths)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestValidateHeldOut(t *testing.T) {
	root := repoWith(t)
	raw := RawPlan{Tasks: []rawTask{
		{
			Title:      "Render per-file diff view",
			TouchPaths: []string{"web/src/views/diff-view.ts"},
			Acceptance: model.Contract{
				VerifyCmds: []string{"cd web && npm test"},
				HeldOut:    []string{"cd web && node --import tsx --test test/heldout/renderDiff.heldout.test.ts"},
			},
		},
		{
			Title: "Fine task",
			Acceptance: model.Contract{
				HeldOut:      []string{"go test -run Hidden ./..."},
				HeldOutFiles: map[string]string{"internal/foo/hidden_test.go": "package foo"},
			},
		},
	}}
	issues := ValidateHeldOut(root, raw)
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %d: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0], "Render per-file diff view") ||
		!strings.Contains(issues[0], "web/test/heldout/renderDiff.heldout.test.ts") {
		t.Fatalf("issue not actionable: %s", issues[0])
	}
}
