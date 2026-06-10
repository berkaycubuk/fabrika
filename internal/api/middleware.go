package api

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

var reportPanic = func(err error) { log.Printf("fabrika: recovered %v", err) }

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw := &recoverWriter{ResponseWriter: w}
		defer func() {
			if rv := recover(); rv != nil {
				err := fmt.Errorf("panic: %v", rv)
				reportPanic(err)
				if !pw.wroteHeader {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(pw, r)
	})
}

type recoverWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recoverWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recoverWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController and
// the WebSocket layer can reach interfaces we don't promote here (notably
// http.Hijacker, which coder/websocket needs to upgrade /api/events).
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// logRequests logs API calls (skips static asset noise) with status + latency.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}
