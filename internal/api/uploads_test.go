package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/model"
	"github.com/berkaycubuk/fabrika/internal/store"
)

// pngBytes is a minimal payload that sniffs as image/png.
var pngBytes = []byte("\x89PNG\r\n\x1a\nrest-of-image")

func doUpload(t *testing.T, h http.Handler, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req := httptest.NewRequest("POST", "/api/uploads", &buf)
	req.Host = "localhost" // pass the same-origin/loopback guard
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestImageUploadAndServe(t *testing.T) {
	h := newTestServer(t)

	rec := doUpload(t, h, pngBytes)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var up struct {
		URL string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &up)
	if !strings.HasPrefix(up.URL, "/api/uploads/") || !strings.HasSuffix(up.URL, ".png") {
		t.Fatalf("upload url = %q", up.URL)
	}

	// The stored image is served back byte-for-byte.
	rec = do(t, h, "GET", up.URL, nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Fatalf("serve: %d (%d bytes)", rec.Code, rec.Body.Len())
	}

	// Application-octet-stream (unrecognised binary) is rejected.
	binPayload := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	if rec := doUpload(t, h, binPayload); rec.Code != http.StatusBadRequest {
		t.Fatalf("binary upload: %d %s", rec.Code, rec.Body.String())
	}

	// Names that aren't generated uploads (e.g. traversal attempts) 404.
	if rec := do(t, h, "GET", "/api/uploads/..%2Ffabrika.db", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("traversal name: %d", rec.Code)
	}
}

func TestEvidenceArtifactServe(t *testing.T) {
	// Engine-deposited evidence artifacts use a wider extension set than human
	// image uploads; the served-name pattern must accept them.
	dir := t.TempDir()
	initRepo(t, dir)
	s, err := store.Open(filepath.Join(dir, "g"), filepath.Join(dir, "p"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := NewServer(s, &config.Config{}, dir, nil, "")
	srv.Start(context.Background())
	h := srv.Handler()

	name := uuid.NewString() + ".log"
	up := filepath.Join(dir, ".fabrika", "uploads")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up, name), []byte("gate output"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, "GET", "/api/uploads/"+name, nil)
	if rec.Code != 200 || rec.Body.String() != "gate output" {
		t.Fatalf("serve artifact: %d %q", rec.Code, rec.Body.String())
	}
	// Artifact URLs are also valid comment attachments.
	if !isUploadURL("/api/uploads/" + name) {
		t.Fatalf("isUploadURL rejected artifact %q", name)
	}
	// Extensions outside the allowlist still 404.
	if rec := do(t, h, "GET", "/api/uploads/"+uuid.NewString()+".sh", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("disallowed ext: %d", rec.Code)
	}
}

func TestCommentAttachments(t *testing.T) {
	h := newTestServer(t)

	rec := do(t, h, "POST", "/api/tasks", model.Task{Title: "t", Spec: "s"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}
	var task model.Task
	json.Unmarshal(rec.Body.Bytes(), &task)

	rec = doUpload(t, h, pngBytes)
	var up struct {
		URL string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &up)

	// An image-only comment (no body) is allowed and round-trips.
	rec = do(t, h, "POST", "/api/tasks/"+task.ID+"/comments", map[string]any{
		"body": "", "attachments": []string{up.URL},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, "GET", "/api/tasks/"+task.ID+"/comments", nil)
	var comments []model.Comment
	json.Unmarshal(rec.Body.Bytes(), &comments)
	if len(comments) != 1 || len(comments[0].Attachments) != 1 || comments[0].Attachments[0] != up.URL {
		t.Fatalf("comments = %+v", comments)
	}

	// Attachment URLs outside /api/uploads are rejected.
	rec = do(t, h, "POST", "/api/tasks/"+task.ID+"/comments", map[string]any{
		"body": "x", "attachments": []string{"https://evil.example/x.png"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad attachment: %d %s", rec.Code, rec.Body.String())
	}

	// Empty comments are still rejected.
	rec = do(t, h, "POST", "/api/tasks/"+task.ID+"/comments", map[string]any{"body": "  "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty comment: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTaskAndBigTaskAttachments(t *testing.T) {
	h := newTestServer(t)

	rec := doUpload(t, h, pngBytes)
	var up struct {
		URL string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &up)

	// Task creation persists attachments and they round-trip via detail.
	rec = do(t, h, "POST", "/api/tasks", model.Task{Title: "t", Attachments: []string{up.URL}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}
	var task model.Task
	json.Unmarshal(rec.Body.Bytes(), &task)
	rec = do(t, h, "GET", "/api/tasks/"+task.ID, nil)
	var detail struct {
		Task model.Task `json:"task"`
	}
	json.Unmarshal(rec.Body.Bytes(), &detail)
	if len(detail.Task.Attachments) != 1 || detail.Task.Attachments[0] != up.URL {
		t.Fatalf("task attachments = %v", detail.Task.Attachments)
	}

	// Attachment URLs outside /api/uploads are rejected on both surfaces.
	rec = do(t, h, "POST", "/api/tasks", model.Task{Title: "t2", Attachments: []string{"https://evil.example/x.png"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad task attachment: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "POST", "/api/bigtasks", model.BigTask{Title: "b2", Attachments: []string{"../../etc/passwd"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad bigtask attachment: %d %s", rec.Code, rec.Body.String())
	}

	// BigTask creation persists attachments; with no planner agent, the
	// passthrough task carries them through to the implementer.
	rec = do(t, h, "POST", "/api/bigtasks", model.BigTask{Title: "bt", Intent: "i", Attachments: []string{up.URL}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bigtask: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/bigtasks", nil)
	var bts []model.BigTask
	json.Unmarshal(rec.Body.Bytes(), &bts)
	if len(bts) != 1 || len(bts[0].Attachments) != 1 || bts[0].Attachments[0] != up.URL {
		t.Fatalf("bigtask attachments = %+v", bts)
	}
	rec = do(t, h, "GET", "/api/tasks", nil)
	var tasks []model.Task
	json.Unmarshal(rec.Body.Bytes(), &tasks)
	var passthrough *model.Task
	for i := range tasks {
		if tasks[i].BigTaskID == bts[0].ID {
			passthrough = &tasks[i]
		}
	}
	if passthrough == nil || len(passthrough.Attachments) != 1 || passthrough.Attachments[0] != up.URL {
		t.Fatalf("passthrough task attachments = %+v", passthrough)
	}
}
