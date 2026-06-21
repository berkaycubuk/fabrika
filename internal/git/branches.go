package git

import (
	"context"
	"strings"
)

// ListBranches returns the local branch names matching the glob pattern (e.g.
// "fabrika/*"). It shells to `git branch --list <pattern>` with a short-name
// format so callers get bare branch names, one per matched ref. Blank lines and
// surrounding whitespace are dropped. A pattern matching nothing returns an
// empty slice with no error.
func (r *Repo) ListBranches(ctx context.Context, pattern string) ([]string, error) {
	out, err := r.run(ctx, "branch", "--list", pattern, "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			branches = append(branches, s)
		}
	}
	return branches, nil
}
