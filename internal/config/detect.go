package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Detection holds the result of auto-detecting the project stack.
type Detection struct {
	Stack   string // "Go", "Node", "Python", "Make", or "" when nothing is detected
	Verbs   Verbs  // pre-filled verbs (zero-value fields stay empty / skipped)
	Message string // human-readable summary printed by the CLI
}

// DetectStack inspects repoRoot for well-known marker files and returns a
// Detection with pre-filled verbs. Precedence: go.mod > package.json >
// pyproject.toml > Makefile (fallback only when no language marker found).
// When a language marker and a Makefile are both present, language verbs are
// used but build/test are overridden with make targets.
func DetectStack(repoRoot string) Detection {
	hasMakefile := statExists(filepath.Join(repoRoot, "Makefile"))

	if statExists(filepath.Join(repoRoot, "go.mod")) {
		v := Verbs{
			Build: "go build ./...",
			Test:  "go test ./...",
			Lint:  "golangci-lint run",
		}
		if hasMakefile {
			v.Build = "make build"
			v.Test = "make test"
		}
		return Detection{
			Stack:   "Go",
			Verbs:   v,
			Message: detectionMessage("Go", v),
		}
	}

	if statExists(filepath.Join(repoRoot, "package.json")) {
		v := Verbs{
			Setup:     "npm install",
			Build:     "npm run build",
			Test:      "npm test",
			Typecheck: "tsc --noEmit",
		}
		if hasMakefile {
			v.Build = "make build"
			v.Test = "make test"
		}
		return Detection{
			Stack:   "Node",
			Verbs:   v,
			Message: detectionMessage("Node", v),
		}
	}

	if statExists(filepath.Join(repoRoot, "pyproject.toml")) {
		v := Verbs{
			Setup: "pip install -e .",
			Test:  "pytest",
			Lint:  "ruff check",
		}
		if hasMakefile {
			v.Test = "make test"
		}
		return Detection{
			Stack:   "Python",
			Verbs:   v,
			Message: detectionMessage("Python", v),
		}
	}

	if hasMakefile {
		v := Verbs{
			Build: "make build",
			Test:  "make test",
		}
		return Detection{
			Stack:   "Make",
			Verbs:   v,
			Message: detectionMessage("Make", v),
		}
	}

	return Detection{}
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// detectionMessage produces the human-readable summary for a Detection.
func detectionMessage(stack string, v Verbs) string {
	var filled []string
	// stable order matching verb declaration order
	if v.Setup != "" {
		filled = append(filled, "setup")
	}
	if v.Build != "" {
		filled = append(filled, "build")
	}
	if v.Test != "" {
		filled = append(filled, "test")
	}
	if v.Lint != "" {
		filled = append(filled, "lint")
	}
	if v.Typecheck != "" {
		filled = append(filled, "typecheck")
	}
	if v.Verify != "" {
		filled = append(filled, "verify")
	}
	if v.E2E != "" {
		filled = append(filled, "e2e")
	}
	if v.Run != "" {
		filled = append(filled, "run")
	}

	noun := stack + " project"
	if stack == "Make" {
		noun = "Makefile"
	}

	return fmt.Sprintf("Detected %s — pre-filled %s verbs", noun, strings.Join(filled, ", "))
}
