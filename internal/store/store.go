// Package store provides SQLite persistence for Fabrika. State is split across
// two single-file databases: a global store (~/.fabrika/fabrika.db) for agent
// definitions and conventions reusable across repos, and a per-project store
// (.fabrika/fabrika.db) for that repo's tasks, runs, and config. See SPECS.md §3.
package store

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrationsFS embed.FS

// Store bundles handles to both databases plus the repos that read/write them.
type Store struct {
	global  *sql.DB
	project *sql.DB

	Agents      *AgentRepo
	Conventions *ConventionRepo
	Settings    *SettingsRepo
	Relay       *RelayRepo
	BigTasks    *BigTaskRepo
	Tasks       *TaskRepo
	Attempts    *AttemptRepo
	Plans       *PlanRepo
	Decisions   *DecisionRepo
	Comments    *CommentRepo
	Releases    *ReleaseRepo
	ActiveRuns  *ActiveRunRepo
	Sessions    *SessionRepo
	Crons       *CronRepo
}

// Open opens (creating if needed) both databases and applies migrations.
//
//	globalDir   typically ~/.fabrika
//	projectDir  typically <repo>/.fabrika
func Open(globalDir, projectDir string) (*Store, error) {
	for _, d := range []string{globalDir, projectDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	global, err := openDB(filepath.Join(globalDir, "fabrika.db"))
	if err != nil {
		return nil, fmt.Errorf("open global db: %w", err)
	}
	if err := migrate(global, "migrations/global"); err != nil {
		global.Close()
		return nil, fmt.Errorf("migrate global db: %w", err)
	}

	project, err := openDB(filepath.Join(projectDir, "fabrika.db"))
	if err != nil {
		global.Close()
		return nil, fmt.Errorf("open project db: %w", err)
	}
	if err := migrate(project, "migrations/project"); err != nil {
		global.Close()
		project.Close()
		return nil, fmt.Errorf("migrate project db: %w", err)
	}

	s := &Store{global: global, project: project}
	s.Agents = &AgentRepo{db: global}
	s.Conventions = &ConventionRepo{db: global}
	s.Settings = &SettingsRepo{db: global}
	s.Relay = &RelayRepo{db: global}
	s.BigTasks = &BigTaskRepo{db: project}
	s.Tasks = &TaskRepo{db: project}
	s.Attempts = &AttemptRepo{db: project}
	s.Plans = &PlanRepo{db: project}
	s.Decisions = &DecisionRepo{db: project}
	s.Comments = &CommentRepo{db: project}
	s.Releases = &ReleaseRepo{db: project}
	s.ActiveRuns = &ActiveRunRepo{db: project}
	s.Sessions = &SessionRepo{db: project}
	s.Crons = &CronRepo{db: project}
	if err := s.Crons.bootstrap(); err != nil {
		global.Close()
		project.Close()
		return nil, fmt.Errorf("bootstrap cron_schedules: %w", err)
	}
	return s, nil
}

// Close closes both databases.
func (s *Store) Close() error {
	var errs []string
	if s.project != nil {
		if err := s.project.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if s.global != nil {
		if err := s.global.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close store: %s", strings.Join(errs, "; "))
	}
	return nil
}

func openDB(path string) (*sql.DB, error) {
	// Pragmas via DSN: WAL for concurrent reads, busy_timeout to ride out locks,
	// foreign_keys on for integrity.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite writes serialize; a single connection avoids "database is locked"
	// churn for our low-concurrency workload while WAL allows concurrent reads.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate applies every *.sql file under dir (in lexical order) that hasn't been
// applied yet, tracked in a schema_migrations table.
func migrate(db *sql.DB, dir string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// jsonStrings encodes a string slice as a JSON array for TEXT storage. A nil
// slice becomes "[]" so columns are never NULL.
func jsonStrings(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// scanStrings decodes a JSON array TEXT column back into a string slice.
func scanStrings(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
