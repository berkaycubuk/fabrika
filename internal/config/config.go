// Package config loads and scaffolds the per-repo fabrika.toml manifest. The
// manifest keeps Fabrika stack-agnostic: it maps abstract verbs to concrete
// commands and declares risk/autonomy policy. Agents live in the UI/global
// store, not here. See SPECS.md §6.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// FileName is the manifest filename expected at the root of a target repo.
const FileName = "fabrika.toml"

// Config is the parsed fabrika.toml.
type Config struct {
	Project  Project  `toml:"project" json:"project"`
	Verbs    Verbs    `toml:"verbs" json:"verbs"`
	Risk     Risk     `toml:"risk" json:"risk"`
	Autonomy Autonomy `toml:"autonomy" json:"autonomy"`
	Deploy   Deploy   `toml:"deploy" json:"deploy"`
	Feedback Feedback `toml:"feedback" json:"feedback"`
}

// Feedback declares optional feedback sources that Fabrika polls for signals.
// An absent [feedback] section (zero sources) is valid and disables the feature.
type Feedback struct {
	Sources []FeedbackSource `toml:"sources" json:"sources"`
}

// FeedbackSource is a single feedback signal provider.
type FeedbackSource struct {
	Type        string `toml:"type" json:"type"`
	Command     string `toml:"command" json:"command"`
	PollSeconds int    `toml:"poll_seconds" json:"pollSeconds"`
}

// Deploy describes how (and whether) to deploy after a merge. An absent
// [deploy] section or an empty Command means deployment is disabled.
type Deploy struct {
	Mode        string `toml:"mode" json:"mode"`
	Command     string `toml:"command" json:"command"`
	Health      string `toml:"health" json:"health"`
	Rollback    string `toml:"rollback" json:"rollback"`
	BakeMinutes int    `toml:"bake_minutes" json:"bakeMinutes"`
}

// Enabled reports whether deployment is configured (non-empty Command).
func (d Deploy) Enabled() bool { return d.Command != "" }

// Project identifies the repo.
type Project struct {
	Name string `toml:"name" json:"name"`
}

// Verbs map abstract gate stages to concrete shell commands. Empty verbs mean
// that stage is skipped. Order of execution is fixed by the gate, not here.
type Verbs struct {
	Setup     string `toml:"setup" json:"setup"`
	Build     string `toml:"build" json:"build"`
	Test      string `toml:"test" json:"test"`
	Lint      string `toml:"lint" json:"lint"`
	Typecheck string `toml:"typecheck" json:"typecheck"`
	Verify    string `toml:"verify" json:"verify"`
	E2E       string `toml:"e2e" json:"e2e"`
	Run       string `toml:"run" json:"run"`
}

// Risk maps path globs to a tier. Anything unmatched is treated as low.
type Risk struct {
	High   []string `toml:"high" json:"high"`
	Medium []string `toml:"medium" json:"medium"`
}

// Autonomy declares which risk tiers auto-merge versus escalate to the human.
type Autonomy struct {
	AutoMerge []string `toml:"auto_merge" json:"auto_merge"`
	Escalate  []string `toml:"escalate" json:"escalate"`
}

// Load reads and parses the manifest at the given repo root.
func Load(repoRoot string) (*Config, error) {
	path := filepath.Join(repoRoot, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Project.Name == "" {
		return nil, fmt.Errorf("%s: [project].name is required", path)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Validate checks [autonomy] and [deploy] semantics.
func (c *Config) Validate() error {
	validTiers := map[string]bool{
		tierLow:    true,
		tierMedium: true,
		tierHigh:   true,
	}
	for _, t := range c.Autonomy.AutoMerge {
		if !validTiers[t] {
			return fmt.Errorf("[autonomy].auto_merge: unknown tier %q (must be low, medium, or high)", t)
		}
	}
	for _, t := range c.Autonomy.Escalate {
		if !validTiers[t] {
			return fmt.Errorf("[autonomy].escalate: unknown tier %q (must be low, medium, or high)", t)
		}
	}
	mergeSet := make(map[string]bool, len(c.Autonomy.AutoMerge))
	for _, t := range c.Autonomy.AutoMerge {
		mergeSet[t] = true
	}
	for _, t := range c.Autonomy.Escalate {
		if mergeSet[t] {
			return fmt.Errorf("[autonomy]: tier %q appears in both auto_merge and escalate", t)
		}
	}

	if c.Deploy.Mode != "" {
		validModes := map[string]bool{
			"manual":   true,
			"per-merge": true,
			"interval": true,
		}
		if !validModes[c.Deploy.Mode] {
			return fmt.Errorf("[deploy].mode: unknown mode %q (must be manual, per-merge, or interval)", c.Deploy.Mode)
		}
	}
	if c.Deploy.BakeMinutes < 0 {
		return fmt.Errorf("[deploy].bake_minutes: must be >= 0, got %d", c.Deploy.BakeMinutes)
	}

	validFeedbackTypes := map[string]bool{
		"command": true,
		"sentry":  true,
	}
	for i, s := range c.Feedback.Sources {
		if !validFeedbackTypes[s.Type] {
			return fmt.Errorf("[feedback].sources[%d].type: unknown type %q (must be command or sentry)", i, s.Type)
		}
		if s.PollSeconds < 10 {
			return fmt.Errorf("[feedback].sources[%d].poll_seconds: must be >= 10, got %d", i, s.PollSeconds)
		}
		if s.Type == "command" && s.Command == "" {
			return fmt.Errorf("[feedback].sources[%d].command: must be non-empty when type is %q", i, s.Type)
		}
	}

	return nil
}

// Exists reports whether a manifest is present at the repo root.
func Exists(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, FileName))
	return err == nil
}

// Scaffold writes a starter fabrika.toml into repoRoot. It refuses to overwrite
// an existing manifest. Used by `fabrika init`.
func Scaffold(repoRoot string) (string, error) {
	path := filepath.Join(repoRoot, FileName)
	if _, err := os.Stat(path); err == nil {
		return path, fmt.Errorf("%s already exists", FileName)
	}
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		return path, fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

const template = `# Fabrika project manifest. Maps abstract verbs to concrete commands so the
# tool stays stack-agnostic. Agents are NOT defined here — manage them in the UI.

[project]
name = "my-app"

[verbs]                       # abstract verb -> concrete command, run in the repo
# setup     = "npm install"
# build     = "npm run build"
# test      = "npm test"
# lint      = "npm run lint -- --max-warnings 0"
# typecheck = "tsc --noEmit"
# verify    = "npm run test:acceptance"
# e2e       = "npx playwright test"
# run       = "npm run dev"

[risk]                        # path globs -> tier; anything unmatched = low
high   = ["**/auth/**", "**/payments/**", "migrations/**", "**/*.sql"]
medium = ["src/api/**"]

[autonomy]
auto_merge = ["low"]          # tiers that merge without you
escalate   = ["medium", "high"]
`
