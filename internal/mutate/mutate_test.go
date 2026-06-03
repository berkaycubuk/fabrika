package mutate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFlipsOperators(t *testing.T) {
	content := "x := a == b\nok := true\nplain line\n"
	got := Generate("f.go", content, 0)
	if len(got) != 2 {
		t.Fatalf("got %d mutants, want 2: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Mutated, "a != b") {
		t.Errorf("first mutant did not flip ==: %q", got[0].Mutated)
	}
	if !strings.Contains(got[1].Mutated, "ok := false") {
		t.Errorf("second mutant did not flip true: %q", got[1].Mutated)
	}
	if got[0].Line != 1 || got[1].Line != 2 {
		t.Errorf("line numbers = %d,%d want 1,2", got[0].Line, got[1].Line)
	}
}

func TestGenerateRespectsMax(t *testing.T) {
	content := "a == b\nc == d\ne == f\n"
	if got := Generate("f", content, 2); len(got) != 2 {
		t.Fatalf("got %d mutants, want capped at 2", len(got))
	}
}

func TestRunCaughtAndRestored(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	original := "x := a == b\n"
	if err := os.WriteFile(file, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// The "suite" passes only while the file still reads "==" — so it catches the
	// == -> != mutant (returns false when mutated).
	test := func(ctx context.Context) bool {
		data, _ := os.ReadFile(file)
		return strings.Contains(string(data), "==")
	}

	res := Run(context.Background(), dir, []string{"f.txt"}, test, 0)
	if res.Tested != 1 || res.Caught != 1 || len(res.Survived) != 0 {
		t.Fatalf("got %+v, want tested=1 caught=1 survived=0", res)
	}
	if !res.Pass() {
		t.Error("expected Pass() with no survivors")
	}
	if data, _ := os.ReadFile(file); string(data) != original {
		t.Errorf("file not restored: %q", data)
	}
}

func TestRunSurvivorFailsPass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x := a == b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A suite that always passes never catches a mutant -> survivor.
	res := Run(context.Background(), dir, []string{"f.txt"}, func(context.Context) bool { return true }, 0)
	if res.Tested != 1 || len(res.Survived) != 1 {
		t.Fatalf("got %+v, want tested=1 survived=1", res)
	}
	if res.Pass() {
		t.Error("expected Pass()==false with a survivor")
	}
}

func TestRunSkipsWithoutTestFunc(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), []string{"f.txt"}, nil, 0)
	if res.Skipped == "" {
		t.Error("expected a skip reason when no test func is given")
	}
	if !res.Pass() {
		t.Error("a skipped run passes vacuously (no evidence of weakness)")
	}
}
