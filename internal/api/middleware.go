package api

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"
)

var reportPanic = func(err error) { log.Printf("fabrika: recovered %v", err) }

// loopbackHost reports whether a "host" or "host:port" value names the local
// machine: the literal "localhost", or any loopback IP (127.0.0.0/8, ::1).
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// guardOrigin defends the loopback-bound API against the two browser-driven
// attacks a localhost server is exposed to:
//
//   - CSRF: any page the user visits can POST to http://localhost:<port>. We
//     reject requests whose Origin header names a host other than localhost, so
//     a cross-origin page can't drive state-changing endpoints (e.g. writing
//     fabrika.toml verbs, which the engine later runs as shell).
//   - DNS rebinding: an attacker domain rebound to 127.0.0.1 talks to the API
//     "same-origin" from the browser's view, bypassing the Origin check on
//     GETs. We also require the Host header itself to be loopback, which a
//     rebound attacker domain (Host: evil.com) fails.
//
// The relay bridge does NOT pass through this guard: it authenticates phones via
// the Noise handshake + method allowlist and replays requests in-process.
func guardOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			log.Printf("fabrika: blocked request with non-loopback Host %q (%s %s)", r.Host, r.Method, r.URL.Path)
			http.Error(w, "forbidden: host not allowed", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
			u, err := url.Parse(origin)
			if err != nil || !loopbackHost(u.Host) {
				log.Printf("fabrika: blocked cross-origin request from %q (%s %s)", origin, r.Method, r.URL.Path)
				http.Error(w, "forbidden: cross-origin request blocked", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

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
