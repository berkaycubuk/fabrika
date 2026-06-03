package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldAndLoad(t *testing.T) {
	dir := t.TempDir()

	if Exists(dir) {
		t.Fatal("manifest should not exist in empty dir")
	}

	path, err := Scaffold(dir)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if filepath.Base(path) != FileName {
		t.Fatalf("unexpected path %q", path)
	}
	if !Exists(dir) {
		t.Fatal("manifest should exist after Scaffold")
	}

	// Scaffold refuses to overwrite.
	if _, err := Scaffold(dir); err == nil {
		t.Fatal("Scaffold should refuse to overwrite existing manifest")
	}

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Project.Name != "my-app" {
		t.Fatalf("project name = %q, want my-app", c.Project.Name)
	}
	if len(c.Autonomy.AutoMerge) != 1 || c.Autonomy.AutoMerge[0] != "low" {
		t.Fatalf("auto_merge = %v, want [low]", c.Autonomy.AutoMerge)
	}
}

func TestLoadVerbs(t *testing.T) {
	dir := t.TempDir()
	manifest := `
[project]
name = "demo"

[verbs]
build = "go build ./..."
test  = "go test ./..."
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Verbs.Build != "go build ./..." {
		t.Fatalf("build verb = %q", c.Verbs.Build)
	}
	if c.Verbs.Lint != "" {
		t.Fatalf("lint verb should be empty (skipped), got %q", c.Verbs.Lint)
	}
}

func TestLoadRequiresName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load should require [project].name")
	}
}
