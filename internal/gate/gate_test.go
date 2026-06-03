package gate

import (
	"context"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestRunOrderAndSkip(t *testing.T) {
	r := New()
	verbs := config.Verbs{
		Build: "true",
		Test:  "true",
	}
	ev, err := r.Run(context.Background(), t.TempDir(), verbs, []string{"true"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// build, test, verify ran and passed.
	for _, s := range []string{"build", "test", "verify"} {
		if !ev.Stages[s].Pass || ev.Stages[s].Skipped {
			t.Fatalf("stage %q = %+v, want passed+not-skipped", s, ev.Stages[s])
		}
	}
	// setup, typecheck, lint, e2e had no verb -> skipped.
	for _, s := range []string{"setup", "typecheck", "lint", "e2e"} {
		if !ev.Stages[s].Skipped {
			t.Fatalf("stage %q should be skipped, got %+v", s, ev.Stages[s])
		}
	}
}

func TestRunStopsOnFailure(t *testing.T) {
	r := New()
	verbs := config.Verbs{
		Build: "false", // fails
		Test:  "true",
	}
	ev, _ := r.Run(context.Background(), t.TempDir(), verbs, nil)

	if ev.Stages["build"].Pass {
		t.Fatal("build should fail")
	}
	if ev.Stages["build"].ExitCode == 0 {
		t.Fatal("failed stage should record non-zero exit code")
	}
	// test comes after build -> skipped because gate stopped.
	if !ev.Stages["test"].Skipped {
		t.Fatalf("test should be skipped after build failure, got %+v", ev.Stages["test"])
	}
}

func TestVerifyCmdsRun(t *testing.T) {
	r := New()
	// No verbs at all, but acceptance commands fail -> verify stage fails.
	ev, _ := r.Run(context.Background(), t.TempDir(), config.Verbs{}, []string{"false"})
	if ev.Stages["verify"].Pass {
		t.Fatalf("verify should fail when an acceptance command fails: %+v", ev.Stages["verify"])
	}
	if ev.Stages["verify"].Skipped {
		t.Fatal("verify should not be skipped when verifyCmds are present")
	}
	_ = model.StageResult{} // keep model import meaningful
}
