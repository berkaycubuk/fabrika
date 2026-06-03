package config

import (
	"path"
	"strings"
)

// Risk tier names. Kept as literals here (rather than importing model) so the
// config package stays dependency-free; they match model.Risk* by value.
const (
	tierLow    = "low"
	tierMedium = "medium"
	tierHigh   = "high"
)

// TierFor computes a task's risk tier from the paths it will touch, matched
// against the manifest's [risk] globs (SPECS.md §6, §9). High wins over medium;
// anything unmatched is low. A task with no declared paths is low.
func (c *Config) TierFor(touchPaths []string) string {
	tier := tierLow
	for _, p := range touchPaths {
		p = strings.TrimPrefix(strings.TrimSpace(p), "./")
		if p == "" {
			continue
		}
		for _, g := range c.Risk.High {
			if matchGlob(g, p) {
				return tierHigh
			}
		}
		for _, g := range c.Risk.Medium {
			if matchGlob(g, p) {
				tier = tierMedium
			}
		}
	}
	return tier
}

// MatchGlob reports whether name matches a path glob that may contain `**`
// (matching zero or more path segments). Exported for reuse (e.g. the engine's
// locked-glob enforcement). See matchGlob.
func MatchGlob(pattern, name string) bool { return matchGlob(pattern, name) }

// matchGlob reports whether name matches a path glob that may contain `**`
// (matching zero or more path segments). Single-segment wildcards (`*`, `?`,
// character classes) within a segment use path.Match semantics. The stdlib
// path.Match alone can't span `/`, so we match segment-by-segment.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true // trailing ** matches any remaining segments
			}
			// ** matches zero or more segments: try every suffix of name.
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
