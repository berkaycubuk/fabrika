package engine

import (
	"strconv"
	"strings"

	"github.com/berkaycubuk/fabrika/internal/mutate"
)

// changedLineRanges parses a unified diff and returns, per new-file path, the
// 1-based line ranges of added ('+') lines only. Context and removed lines are
// excluded. Files that have no added lines are absent from the map. A nil or
// empty map passed to mutate.RunScoped triggers full-file mutation, so a diff
// with no additions reproduces the un-scoped Run behaviour.
func changedLineRanges(diff string) map[string][]mutate.LineRange {
	out := map[string][]mutate.LineRange{}
	var curFile string
	var lineNo int
	inHunk := false

	for _, line := range strings.Split(diff, "\n") {
		// Hunk header: always starts a new hunk, regardless of current mode.
		if strings.HasPrefix(line, "@@ ") {
			if n := parseNewStart(line); n > 0 {
				lineNo = n
				inHunk = true
			} else {
				inHunk = false
			}
			continue
		}

		// Outside hunk mode: look for file headers only.
		if !inHunk {
			if strings.HasPrefix(line, "+++ ") {
				rest := strings.TrimPrefix(line, "+++ ")
				if rest == "/dev/null" {
					curFile = "" // pure deletion — no new-file lines to record
				} else {
					rest = strings.TrimPrefix(rest, "b/")
					rest = strings.TrimPrefix(rest, "a/")
					curFile = rest
				}
			}
			// "--- ", "diff ", "index ", etc. are preamble — skip silently.
			continue
		}

		// Inside hunk mode: classify each diff line.
		switch {
		case strings.HasPrefix(line, "+"):
			// Added line: record its new-file position and advance the counter.
			if curFile != "" {
				out[curFile] = append(out[curFile], mutate.LineRange{Start: lineNo, End: lineNo})
			}
			lineNo++
		case strings.HasPrefix(line, "-"):
			// Removed line: exists only in old file, counter does not advance.
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — metadata, not a real line.
		default:
			// Context line (space prefix): present in both files, advance counter.
			lineNo++
		}
	}

	return out
}

// parseNewStart extracts the new-file starting line number from a unified-diff
// hunk header of the form "@@ -<old>[,<count>] +<new>[,<count>] @@".
// Returns 0 when the header cannot be parsed.
func parseNewStart(hdr string) int {
	i := strings.Index(hdr, " +")
	if i < 0 {
		return 0
	}
	s := hdr[i+2:] // skip " +"
	if j := strings.IndexAny(s, ", \t"); j >= 0 {
		s = s[:j]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
