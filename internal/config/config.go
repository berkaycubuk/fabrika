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
	Project  Project  `toml:"project"`
	Verbs    Verbs    `toml:"verbs"`
	Risk     Risk     `toml:"risk"`
	Autonomy Autonomy `toml:"autonomy"`
}

// Project identifies the repo.
type Project struct {
	Name string `toml:"name"`
}

// Verbs map abstract gate stages to concrete shell commands. Empty verbs mean
// that stage is skipped. Order of execution is fixed by the gate, not here.
type Verbs struct {
	Setup     string `toml:"setup"`
	Build     string `toml:"build"`
	Test      string `toml:"test"`
	Lint      string `toml:"lint"`
	Typecheck string `toml:"typecheck"`
	Verify    string `toml:"verify"`
	E2E       string `toml:"e2e"`
	Run       string `toml:"run"`
}

// Risk maps path globs to a tier. Anything unmatched is treated as low.
type Risk struct {
	High   []string `toml:"high"`
	Medium []string `toml:"medium"`
}

// Autonomy declares which risk tiers auto-merge versus escalate to the human.
type Autonomy struct {
	AutoMerge []string `toml:"auto_merge"`
	Escalate  []string `toml:"escalate"`
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

// Validate checks [autonomy] semantics: every listed tier must be "low",
// "medium", or "high", and no tier may appear in both auto_merge and escalate.
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
