package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistsDocumentsAndBoundsList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gpup.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put("target", "local", map[string]string{"name": "local"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		if err = s.Put("run", id, map[string]string{"id": id}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var target map[string]string
	if err = s.Get("target", "local", &target); err != nil || target["name"] != "local" {
		t.Fatalf("roundtrip: %v %v", target, err)
	}
	rows, err := s.List("run", 2)
	if err != nil || len(rows) != 2 {
		t.Fatalf("bound: %d %v", len(rows), err)
	}
	if !json.Valid(rows[0]) {
		t.Fatal("invalid JSON")
	}
	if err = s.Delete("target", "local"); err != nil {
		t.Fatal(err)
	}
	if err = s.Get("target", "local", &target); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestRejectsInvalidStorageInputs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gpup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Put("run", "", map[string]int{}); err == nil {
		t.Fatal("empty ID accepted")
	}
	if _, err = s.List("run", 10001); err == nil {
		t.Fatal("unbounded list")
	}
	if _, err = s.List("run", 0); err == nil {
		t.Fatal("zero list")
	}
}

func TestSnapshotRetention(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "gpup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.AddSnapshot(time.Now().Add(-25*time.Hour), map[string]int{"old": 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.AddSnapshot(time.Now(), map[string]int{"new": 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(24*time.Hour, 2); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Snapshots(5)
	if err != nil || len(rows) != 1 {
		t.Fatalf("retention: %d %v", len(rows), err)
	}
}
