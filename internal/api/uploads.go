package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Uploaded images live under <repo>/.fabrika/uploads, named <uuid>.<ext> so the
// served name can be validated with a strict pattern (no path traversal).
const maxUploadBytes = 10 << 20 // 10 MiB

// The extension set covers human image uploads plus engine-deposited evidence
// artifacts (screenshots/recordings/logs); see internal/engine evidenceExts.
var uploadNameRe = regexp.MustCompile(`^[a-f0-9-]{36}\.(png|jpg|jpeg|gif|webp|txt|log|json|mp4|webm)$`)

// extByType maps the sniffed content type to the stored extension; doubling as
// the allowlist of accepted image formats.
var extByType = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

func (s *Server) uploadsDir() string {
	return filepath.Join(s.repoRoot, ".fabrika", "uploads")
}

// createUpload accepts one multipart image ("file" field), stores it under the
// project's uploads dir, and returns the URL it will be served from.
func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected multipart form with a 'file' field")
		return
	}
	defer file.Close()

	// Sniff the real content type from the first bytes; the client-supplied
	// header and filename are untrusted.
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && err != io.ErrUnexpectedEOF {
		writeErr(w, http.StatusBadRequest, "unreadable upload")
		return
	}
	ext, ok := extByType[http.DetectContentType(head[:n])]
	if !ok {
		writeErr(w, http.StatusBadRequest, "only png, jpeg, gif, or webp images are accepted")
		return
	}

	if err := os.MkdirAll(s.uploadsDir(), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fmt.Sprintf("%s.%s", uuid.NewString(), ext)
	dst, err := os.Create(filepath.Join(s.uploadsDir(), name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer dst.Close()
	if _, err := dst.Write(head[:n]); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(dst.Name())
		writeErr(w, http.StatusRequestEntityTooLarge, "image exceeds the 10 MiB limit")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": uploadURL(name)})
}

// getUpload serves a previously uploaded image by its generated name.
func (s *Server) getUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !uploadNameRe.MatchString(name) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	path := filepath.Join(s.uploadsDir(), name)
	if _, err := os.Stat(path); err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// Uploads are immutable (uuid names), so let the browser cache them.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

func uploadURL(name string) string { return "/api/uploads/" + name }

// isUploadURL reports whether u is a URL produced by createUpload; comment
// attachments must pass this so arbitrary strings can't be persisted.
func isUploadURL(u string) bool {
	name, ok := strings.CutPrefix(u, "/api/uploads/")
	return ok && uploadNameRe.MatchString(name)
}
