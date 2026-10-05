// Package store keeps jobs, series and settings in a SQLite database.
// Each record is stored as JSON; SQLite provides crash-safe writes.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
)

// ErrNotFound is returned for a job or series that doesn't exist.
var ErrNotFound = errors.New("not found")

// Store is the app's database. It is safe for concurrent use.
type Store struct {
	db *sql.DB
	// mu serializes read-modify-write sequences such as stem assignment.
	mu sync.Mutex
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, data TEXT NOT NULL, created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS series (name TEXT PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY CHECK (id = 1), data TEXT NOT NULL);
`

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Settings returns the saved settings, or the defaults if none are saved.
func (s *Store) Settings() (jobs.Settings, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM settings WHERE id = 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.DefaultSettings(), nil
	}
	if err != nil {
		return jobs.Settings{}, err
	}
	set := jobs.DefaultSettings()
	return set, json.Unmarshal([]byte(data), &set)
}

// SaveSettings replaces the settings.
func (s *Store) SaveSettings(set jobs.Settings) error {
	data, err := json.Marshal(set)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, string(data))
	return err
}

// Series returns every series, sorted by name.
func (s *Store) Series() ([]jobs.Series, error) {
	rows, err := s.db.Query(`SELECT data FROM series ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []jobs.Series
	for rows.Next() {
		var data string
		var sr jobs.Series
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &sr); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// FindSeries returns the series called name.
func (s *Store) FindSeries(name string) (jobs.Series, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM series WHERE name = ?`, name).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.Series{}, ErrNotFound
	}
	if err != nil {
		return jobs.Series{}, err
	}
	var sr jobs.Series
	return sr, json.Unmarshal([]byte(data), &sr)
}

// SaveSeries adds or replaces a series by name.
func (s *Store) SaveSeries(sr jobs.Series) error {
	if sr.Name == "" {
		return errors.New("a series needs a name")
	}
	data, err := json.Marshal(sr)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO series (name, data) VALUES (?, ?) ON CONFLICT(name) DO UPDATE SET data = excluded.data`, sr.Name, string(data))
	return err
}

// DeleteSeries removes a series.
func (s *Store) DeleteSeries(name string) error {
	_, err := s.db.Exec(`DELETE FROM series WHERE name = ?`, name)
	return err
}

// Jobs returns every job, oldest first.
func (s *Store) Jobs() ([]*jobs.Job, error) {
	rows, err := s.db.Query(`SELECT data FROM jobs ORDER BY created, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*jobs.Job
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		j := &jobs.Job{}
		if err := json.Unmarshal([]byte(data), j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Job returns the job with id.
func (s *Store) Job(id string) (*jobs.Job, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM jobs WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j := &jobs.Job{}
	return j, json.Unmarshal([]byte(data), j)
}

// SaveJob adds or replaces j, first giving it an output stem that no
// other job uses (see jobs.AssignStem).
func (s *Store) SaveJob(j *jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.Jobs()
	if err != nil {
		return err
	}
	set, err := s.Settings()
	if err != nil {
		return err
	}
	jobs.AssignStem(j, all, set)
	return s.put(j)
}

// UpdateJob applies change to the stored job with id and saves it. Use it
// for status changes that must not race with other writers.
func (s *Store) UpdateJob(id string, change func(*jobs.Job)) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.Job(id)
	if err != nil {
		return nil, err
	}
	change(j)
	return j, s.put(j)
}

func (s *Store) put(j *jobs.Job) error {
	j.Updated = time.Now()
	if j.Created.IsZero() {
		j.Created = j.Updated
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO jobs (id, data, created) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`,
		j.ID, string(data), j.Created.UnixNano())
	return err
}

// DeleteJob removes a job.
func (s *Store) DeleteJob(id string) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	return err
}

// RecoverInterrupted returns jobs left queued or rendering by a crash or
// forced quit to their resting status, noting the interruption.
func (s *Store) RecoverInterrupted() ([]string, error) {
	all, err := s.Jobs()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, j := range all {
		switch j.Status {
		case jobs.StatusQueued, jobs.StatusTrimming, jobs.StatusStitching:
			_, err := s.UpdateJob(j.ID, func(j *jobs.Job) {
				if j.Status != jobs.StatusQueued {
					j.Error = "interrupted: the app closed while it was rendering"
				}
				j.Status = j.BaseStatus()
			})
			if err != nil {
				return ids, err
			}
			ids = append(ids, j.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
