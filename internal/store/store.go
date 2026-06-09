package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/durck/reverse_logger/internal/events"
	_ "modernc.org/sqlite"
)

type Store struct {
	db          *sql.DB
	eventsPath  string
	edgeLogPath string
}

func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		dataDir = "/data"
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", filepath.Join(dataDir, "events.db"))
	if err != nil {
		return nil, err
	}

	store := &Store{
		db:          db,
		eventsPath:  filepath.Join(dataDir, "events.jsonl"),
		edgeLogPath: filepath.Join(dataDir, "edge_events.jsonl"),
	}

	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) init() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}

	schema := `
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_hash TEXT NOT NULL UNIQUE,
	status TEXT NOT NULL,
	reverse_ssh_id TEXT,
	host_name TEXT,
	user_name TEXT,
	computer_name TEXT,
	ip_raw TEXT,
	ip_addr TEXT,
	ip_port INTEGER,
	version TEXT,
	source_ts TEXT,
	received_at TEXT NOT NULL,
	raw_json TEXT
);

CREATE TABLE IF NOT EXISTS edge_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_hash TEXT NOT NULL UNIQUE,
	vps_name TEXT NOT NULL,
	vps_public_ip TEXT,
	vps_port INTEGER,
	client_ip TEXT NOT NULL,
	client_port INTEGER,
	received_at TEXT NOT NULL,
	raw_json TEXT
);`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) InsertEvent(event events.Event) (bool, error) {
	sourceTS := ""
	if !event.SourceTS.IsZero() {
		sourceTS = event.SourceTS.UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
INSERT OR IGNORE INTO events (
	event_hash, status, reverse_ssh_id, host_name, user_name, computer_name,
	ip_raw, ip_addr, ip_port, version, source_ts, received_at, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventHash,
		event.Status,
		event.ReverseSSHID,
		event.HostName,
		event.UserName,
		event.ComputerName,
		event.IPRaw,
		event.IPAddr,
		event.IPPort,
		event.Version,
		sourceTS,
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		string(event.RawJSON),
	)
	if err != nil {
		return false, err
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := appendJSONL(s.eventsPath, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) InsertEdgeEvent(event events.EdgeEvent) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
INSERT OR IGNORE INTO edge_events (
	event_hash, vps_name, vps_public_ip, vps_port,
	client_ip, client_port, received_at, raw_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventHash,
		event.VPSName,
		event.VPSPublicIP,
		event.VPSPort,
		event.ClientIP,
		event.ClientPort,
		event.ReceivedAt.UTC().Format(time.RFC3339Nano),
		string(event.RawJSON),
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := appendJSONL(s.edgeLogPath, event); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Ping() error {
	return s.db.Ping()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func appendJSONL(path string, value any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}
