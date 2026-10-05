package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path != " :memory:" && path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", `CREATE TABLE IF NOT EXISTS documents (kind TEXT NOT NULL, id TEXT NOT NULL, body BLOB NOT NULL, updated INTEGER NOT NULL, PRIMARY KEY(kind,id))`, `CREATE INDEX IF NOT EXISTS documents_updated ON documents(kind,updated DESC)`, `CREATE TABLE IF NOT EXISTS snapshots (id INTEGER PRIMARY KEY, timestamp INTEGER NOT NULL, body BLOB NOT NULL)`, `CREATE INDEX IF NOT EXISTS snapshots_time ON snapshots(timestamp)`} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize storage: %w", err)
		}
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Put(kind, id string, v any) error {
	if kind == "" || id == "" || len(id) > 200 {
		return errors.New("kind and bounded ID required")
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO documents(kind,id,body,updated) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body,updated=excluded.updated`, kind, id, body, time.Now().UnixNano())
	return err
}
func (s *Store) Get(kind, id string, v any) error {
	var body []byte
	err := s.db.QueryRow(`SELECT body FROM documents WHERE kind=? AND id=?`, kind, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
func (s *Store) Delete(kind, id string) error {
	_, err := s.db.Exec(`DELETE FROM documents WHERE kind=? AND id=?`, kind, id)
	return err
}
func (s *Store) List(kind string, limit int) ([]json.RawMessage, error) {
	if limit < 1 || limit > 10000 {
		return nil, errors.New("limit must be 1..10000")
	}
	rows, err := s.db.Query(`SELECT body FROM documents WHERE kind=? ORDER BY updated DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(body))
	}
	return out, rows.Err()
}
func (s *Store) AddSnapshot(at time.Time, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO snapshots(timestamp,body) VALUES(?,?)`, at.Unix(), b)
	return err
}
func (s *Store) Snapshots(limit int) ([]json.RawMessage, error) {
	if limit < 1 || limit > 10000 {
		return nil, errors.New("limit must be 1..10000")
	}
	rows, err := s.db.Query(`SELECT body FROM snapshots ORDER BY timestamp DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
func (s *Store) Prune(rawRetention time.Duration, maxRuns int) error {
	if rawRetention <= 0 || maxRuns < 1 {
		return errors.New("positive retention required")
	}
	if _, err := s.db.Exec(`DELETE FROM snapshots WHERE timestamp < ?`, time.Now().Add(-rawRetention).Unix()); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM documents WHERE kind='run' AND id NOT IN (SELECT id FROM documents WHERE kind='run' ORDER BY updated DESC LIMIT ?)`, maxRuns)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM documents WHERE kind='requests' AND id NOT IN (SELECT id FROM documents WHERE kind='run')`)
	return err
}
