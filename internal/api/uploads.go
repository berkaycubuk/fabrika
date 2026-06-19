package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// Uploaded files live under <repo>/.fabrika/uploads, named <uuid>.<ext> so the
// served name can be validated with a strict pattern (no path traversal).
const maxUploadBytes = 25 << 20 // 25 MiB

// The 36-char uuid prefix (not the extension) is what blocks path traversal, so
// the served-name pattern only needs a permissive extension group wide enough to
// cover every type createUpload stores plus engine-deposited evidence artifacts
// (screenshots/recordings/logs); see internal/engine evidenceExts.
var uploadNameRe = regexp.MustCompile(`^[a-f0-9-]{36}\.[a-z0-9]{1,12}$`)

// imageExts are accepted only when http.DetectContentType confirms the payload
// really is an image; this is what keeps a text body with a .png name out.
var imageExts = map[string]bool{
	"png":  true,
	"jpg":  true,
	"jpeg": true,
	"gif":  true,
	"webp": true,
}

// fileExts are non-image types accepted by extension (no content sniff).
// Executables and scripts (exe, bat, cmd, com, sh, dll, so, ...) are
// deliberately absent so they cannot be stored.
var fileExts = map[string]bool{
	"txt":  true,
	"log":  true,
	"json": true,
	"csv":  true,
	"md":   true,
	"pdf":  true,
	"zip":  true,
	"gz":   true,
	"tgz":  true,
	"tar":  true,
	"yaml": true,
	"yml":  true,
	"go":   true,
	"js":   true,
	"ts":   true,
	"py":   true,
	"mp4":  true,
	"webm": true,
}

func (s *Server) uploadsDir() string {
	return filepath.Join(s.repoRoot, ".fabrika", "uploads")
}

// createUpload accepts one multipart file ("file" field), stores it under the
// project's uploads dir, records its metadata, and returns the URL it will be
// served from. Image types are content-sniffed; other allowed types are
// accepted by their original filename's extension.
func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected multipart form with a 'file' field")
		return
	}
	defer file.Close()

	// The original filename is untrusted, but its extension selects the policy.
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
	isImage := imageExts[ext]
	if !isImage && !fileExts[ext] {
		writeErr(w, http.StatusBadRequest, "file type ."+ext+" is not accepted")
		return
	}

	// contentType is what we serve the file back as: the sniffed type for
	// images, or the extension's registered MIME type for other files.
	var contentType string
	var head []byte
	if isImage {
		// Sniff the real content type from the first bytes; a non-image payload
		// wearing an image extension is rejected.
		buf := make([]byte, 512)
		n, err := io.ReadFull(file, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			writeErr(w, http.StatusBadRequest, "unreadable upload")
			return
		}
		head = buf[:n]
		ct := http.DetectContentType(head)
		if i := strings.IndexByte(ct, ';'); i != -1 {
			ct = strings.TrimSpace(ct[:i])
		}
		if !strings.HasPrefix(ct, "image/") {
			writeErr(w, http.StatusBadRequest, "file is not a valid image")
			return
		}
		contentType = ct
	} else {
		contentType = mime.TypeByExtension("." + ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
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
	var size int64
	if len(head) > 0 {
		nw, err := dst.Write(head)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		size += int64(nw)
	}
	nc, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(dst.Name())
		writeErr(w, http.StatusRequestEntityTooLarge, "file exceeds the 25 MiB limit")
		return
	}
	size += nc

	if err := s.store.Uploads.Create(&model.Upload{
		Name:        name,
		Filename:    header.Filename,
		ContentType: contentType,
		Size:        size,
	}); err != nil {
		os.Remove(dst.Name())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": uploadURL(name)})
}

// getUpload serves a previously uploaded file by its generated name. Images are
// served inline so previews work; other files are served as attachments with
// their original filename so the browser downloads them.
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

	// Engine-deposited evidence artifacts go straight to disk with no upload
	// row; for those we fall back to ServeFile's own type detection.
	up, err := s.store.Uploads.Get(name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if up != nil {
		if up.ContentType != "" {
			w.Header().Set("Content-Type", up.ContentType)
		}
		if !strings.HasPrefix(up.ContentType, "image/") {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", up.Filename))
		}
	}
	http.ServeFile(w, r, path)
}

func uploadURL(name string) string { return "/api/uploads/" + name }

// isUploadURL reports whether u is a URL produced by createUpload; comment
// attachments must pass this so arbitrary strings can't be persisted.
func isUploadURL(u string) bool {
	name, ok := strings.CutPrefix(u, "/api/uploads/")
	return ok && uploadNameRe.MatchString(name)
}
