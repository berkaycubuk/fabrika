package engine

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/berkaycubuk/fabrika/internal/agent"
)

// Evidence artifacts an agent points at (fabrika_EVIDENCE: lines) are copied out
// of its worktree into <repoRoot>/.fabrika/uploads — the same store the api layer
// serves at /api/uploads/<name> — before the worktree is cleaned up. The engine
// writes files directly (api imports engine, so it cannot import api).

// maxEvidenceBytes caps a single artifact; recordings can be large, so this is
// looser than the 10 MiB human image-upload limit.
const maxEvidenceBytes = 25 << 20

// evidenceExts is the allowlist of artifact file types the engine will ingest.
// The served-name pattern in the api layer must accept the same set.
var evidenceExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".txt": true, ".log": true, ".json": true, ".mp4": true, ".webm": true,
}

func (e *Engine) uploadsDir() string {
	return filepath.Join(e.repoRoot, ".fabrika", "uploads")
}

// ingestEvidence copies each referenced worktree file into the uploads dir and
// returns the served URLs plus a url->caption map. Bad references (missing,
// outside the worktree, oversized, disallowed type) are skipped and logged — a
// broken screenshot must never fail a run.
func (e *Engine) ingestEvidence(worktree string, refs []agent.EvidenceRef) ([]string, map[string]string) {
	if len(refs) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(e.uploadsDir(), 0o755); err != nil {
		log.Printf("engine: evidence uploads dir: %v", err)
		return nil, nil
	}
	var urls []string
	captions := map[string]string{}
	for _, ref := range refs {
		url, err := copyArtifact(worktree, ref.Path, e.uploadsDir())
		if err != nil {
			log.Printf("engine: evidence %q skipped: %v", ref.Path, err)
			continue
		}
		urls = append(urls, url)
		if ref.Caption != "" {
			captions[url] = ref.Caption
		}
	}
	return urls, captions
}

// copyArtifact validates one evidence path against root (the worktree) and
// copies it into uploadsDir under a generated <uuid>.<ext> name, returning the
// /api/uploads/<name> URL it will be served from.
func copyArtifact(root, p, uploadsDir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(p))
	if !evidenceExts[ext] {
		return "", fmt.Errorf("disallowed type %q", ext)
	}

	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	// Resolve symlinks before the containment check so a link inside the
	// worktree can't smuggle a file from outside it.
	abs, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes worktree")
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	if info.Size() > maxEvidenceBytes {
		return "", fmt.Errorf("exceeds %d MiB limit", maxEvidenceBytes>>20)
	}

	src, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer src.Close()
	name := uuid.NewString() + ext
	dst, err := os.Create(filepath.Join(uploadsDir, name))
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return "/api/uploads/" + name, nil
}

// evidenceCommentBody renders the body of the agent comment that carries the
// run's artifacts: a heading plus one line per captioned artifact.
func evidenceCommentBody(urls []string, captions map[string]string) string {
	var b strings.Builder
	b.WriteString("Evidence")
	for _, u := range urls {
		if c := captions[u]; c != "" {
			b.WriteString("\n- " + c)
		}
	}
	return b.String()
}
