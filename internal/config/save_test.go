package config

import (
	"reflect"
	"testing"
)

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Config{
		Project: Project{Name: "demo"},
		Verbs: Verbs{
			Build: "go build ./...",
			Test:  "go test ./...",
		},
		Risk: Risk{
			High:   []string{"**/auth/**"},
			Medium: []string{"src/api/**"},
		},
		Autonomy: Autonomy{
			AutoMerge: []string{"low"},
			Escalate:  []string{"medium", "high"},
		},
	}

	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	bad := &Config{
		Project:  Project{Name: "demo"},
		Autonomy: Autonomy{AutoMerge: []string{"bogus"}},
	}
	if err := Save(dir, bad); err == nil {
		t.Fatal("Save should reject invalid config")
	}
	if Exists(dir) {
		t.Fatal("Save must not write when validation fails")
	}
}
