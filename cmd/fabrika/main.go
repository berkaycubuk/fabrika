// Command fabrika is a local, single-binary orchestrator for coding agents.
// Run it from inside a target repo that has a fabrika.toml; it starts a local
// HTTP server, opens the cockpit UI in the browser, and operates on the repo via
// local git + subprocess access. See SPECS.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/berkaycubuk/fabrika/internal/api"
	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/observability"
	"github.com/berkaycubuk/fabrika/internal/store"
	"github.com/berkaycubuk/fabrika/web"
)

const defaultPort = 7777

// version is stamped at release-build time via
// `-ldflags "-X main.version=v0.1.0"`. Dev builds fall back to the VCS
// revision the Go toolchain embeds automatically.
var version = "dev"

// versionString returns the stamped release version, or "dev (<commit>)" when
// built without ldflags (e.g. plain `go build` / `go run`).
func versionString() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return "dev (" + rev + dirty + ")"
		}
	}
	return version
}

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fabrika: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// Subcommand dispatch: `fabrika init` scaffolds a manifest,
	// `fabrika version` prints the build version.
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return cmdInit()
		case "version":
			fmt.Println("fabrika " + versionString())
			return nil
		}
	}

	fs := flag.NewFlagSet("fabrika", flag.ContinueOnError)
	port := fs.Int("port", defaultPort, "HTTP port for the UI/API")
	noOpen := fs.Bool("no-open", false, "do not open the browser on start")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("fabrika " + versionString())
		return nil
	}

	return cmdServe(*port, !*noOpen)
}

// cmdInit scaffolds a fabrika.toml in the current directory.
func cmdInit() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	path, err := config.Scaffold(cwd)
	if err != nil {
		return err
	}
	fmt.Printf("Created %s\n", path)
	if d := config.DetectStack(cwd); d.Stack != "" {
		fmt.Println(d.Message)
	} else {
		fmt.Println("Edit it to map your repo's build/verify verbs, then run `fabrika`.")
	}
	return nil
}

// cmdServe loads the manifest, opens the stores, and serves the UI/API until
// interrupted.
func cmdServe(port int, openBrowser bool) error {
	env := "production"
	if version == "dev" {
		env = "development"
	}
	flush, err := observability.Init(observability.ResolveDSN(), versionString(), env)
	if err != nil {
		log.Printf("fabrika: sentry init: %v", err)
	}
	defer flush()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if !config.Exists(cwd) {
		return fmt.Errorf("no %s in %s — run `fabrika init` first", config.FileName, cwd)
	}
	cfg, err := config.Load(cwd)
	if err != nil {
		return err
	}

	globalDir, err := globalStoreDir()
	if err != nil {
		return err
	}
	projectDir := filepath.Join(cwd, ".fabrika")

	st, err := store.Open(globalDir, projectDir)
	if err != nil {
		return err
	}
	defer st.Close()

	assets, err := web.Assets()
	if err != nil {
		return fmt.Errorf("load embedded UI: %w", err)
	}

	// Lifecycle context: cancelled on interrupt, also stops the engine loop and
	// any in-flight agent subprocess.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := api.NewServer(st, cfg, cwd, assets, versionString())
	srv.Start(ctx) // launch the dispatch loop

	addr := fmt.Sprintf("localhost:%d", port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	url := "http://" + addr
	log.Printf("fabrika: %s", versionString())
	log.Printf("fabrika: project %q", cfg.Project.Name)
	log.Printf("fabrika: global store  %s", filepath.Join(globalDir, "fabrika.db"))
	log.Printf("fabrika: project store %s", filepath.Join(projectDir, "fabrika.db"))
	log.Printf("fabrika: serving %s", url)

	// Start the server.
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	if openBrowser {
		// Give the listener a beat, then open the browser.
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openURL(url); err != nil {
				log.Printf("fabrika: open browser: %v (visit %s manually)", err, url)
			}
		}()
	}

	// Wait for interrupt or server error.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Println("\nfabrika: shutting down…")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutCtx)
	}
}

// globalStoreDir returns ~/.fabrika, creating it if needed.
func globalStoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fabrika"), nil
}

// openURL opens a URL in the default browser, per-platform.
func openURL(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, bsd, …
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
