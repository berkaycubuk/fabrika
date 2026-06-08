package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func openBench(b *testing.B) *Store {
	b.Helper()
	dir := b.TempDir()
	s, err := Open(filepath.Join(dir, "global"), filepath.Join(dir, "project"))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

func seedTasks(b *testing.B, s *Store) {
	b.Helper()
	for i := 0; i < 140; i++ {
		t := &model.Task{Title: fmt.Sprintf("merged-%d", i), Status: model.TaskMerged}
		if err := s.Tasks.Create(t); err != nil {
			b.Fatalf("seed merged task: %v", err)
		}
	}
	for i := 0; i < 10; i++ {
		t := &model.Task{Title: fmt.Sprintf("ready-%d", i), Status: model.TaskReady}
		if err := s.Tasks.Create(t); err != nil {
			b.Fatalf("seed ready task: %v", err)
		}
	}
}

func BenchmarkTasksList(b *testing.B) {
	s := openBench(b)
	seedTasks(b, s)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Tasks.List(); err != nil {
			b.Fatalf("List: %v", err)
		}
	}
}

func BenchmarkTasksListByStatus(b *testing.B) {
	s := openBench(b)
	seedTasks(b, s)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Tasks.ListByStatus(model.TaskReady); err != nil {
			b.Fatalf("ListByStatus: %v", err)
		}
	}
}
