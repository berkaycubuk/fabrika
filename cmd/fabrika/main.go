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
	"syscall"
	"time"

	"github.com/berkaycubuk/fabrika/internal/api"
	"github.com/berkaycubuk/fabrika/internal/config"
	"github.com/berkaycubuk/fabrika/internal/store"
	"github.com/berkaycubuk/fabrika/web"
)

const defaultPort = 7777

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fabrika: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// Subcommand dispatch: `fabrika init` scaffolds a manifest.
	if len(args) > 0 && args[0] == "init" {
		return cmdInit()
	}

	fs := flag.NewFlagSet("fabrika", flag.ContinueOnError)
	port := fs.Int("port", defaultPort, "HTTP port for the UI/API")
	noOpen := fs.Bool("no-open", false, "do not open the browser on start")
	if err := fs.Parse(args); err != nil {
		return err
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
	fmt.Println("Edit it to map your repo's build/verify verbs, then run `fabrika`.")
	return nil
}

// cmdServe loads the manifest, opens the stores, and serves the UI/API until
// interrupted.
func cmdServe(port int, openBrowser bool) error {
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

	srv := api.NewServer(st, assets)
	addr := fmt.Sprintf("localhost:%d", port)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	url := "http://" + addr
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
