// Package store persists the single-owner bot state in SQLite.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/qrave1/atlas/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func New(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// A single durable snapshot is sufficient for one owner and one sequential worker.
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
	CREATE TABLE IF NOT EXISTS state (id INTEGER PRIMARY KEY CHECK(id=1), value BLOB NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	_, err = s.Load(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		state := model.State{InstallationID: NewID(), Owned: map[string]bool{}}
		err = s.Save(ctx, state)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (s *Store) Load(ctx context.Context) (model.State, error) {
	var raw []byte
	var state model.State
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM state WHERE id=1").Scan(&raw); err != nil {
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Owned == nil {
		state.Owned = map[string]bool{}
	}
	return state, nil
}

func (s *Store) Save(ctx context.Context, state model.State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO state(id,value) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value", raw)
	return err
}

func (s *Store) Close() error { return s.db.Close() }
