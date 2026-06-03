package config

import "testing"

func TestTierFor(t *testing.T) {
	c := &Config{Risk: Risk{
		High:   []string{"**/auth/**", "**/payments/**", "migrations/**", "**/*.sql"},
		Medium: []string{"src/api/**"},
	}}

	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"unmatched is low", []string{"src/util/helpers.go"}, "low"},
		{"no paths is low", nil, "low"},
		{"nested auth is high", []string{"internal/auth/login.go"}, "high"},
		{"top-level auth is high", []string{"auth/x.go"}, "high"},
		{"migrations prefix is high", []string{"migrations/001_init.sql"}, "high"},
		{"sql anywhere is high", []string{"db/schema.sql"}, "high"},
		{"api dir is medium", []string{"src/api/users.go"}, "medium"},
		{"high beats medium", []string{"src/api/users.go", "internal/auth/x.go"}, "high"},
		{"leading dot-slash normalized", []string{"./src/api/users.go"}, "medium"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.TierFor(tc.paths); got != tc.want {
				t.Fatalf("TierFor(%v) = %q, want %q", tc.paths, got, tc.want)
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"**/auth/**", "a/b/auth/c/d.go", true},
		{"**/auth/**", "auth/c.go", true},
		{"migrations/**", "migrations", true},
		{"migrations/**", "migrations/001.sql", true},
		{"migrations/**", "src/migrations/001.sql", false},
		{"**/*.sql", "x/y/z.sql", true},
		{"**/*.sql", "z.sql", true},
		{"src/api/**", "src/api/v1/users.go", true},
		{"src/api/**", "src/web/users.go", false},
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}
