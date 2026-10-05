// Package backlog handles a folder of past recordings to mark. Its marks
// are kept in a backlog.json inside the folder, with paths relative to it,
// so the folder can be marked on one machine and rendered on another.
package backlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
	"github.com/summitlimestone/subsplash-generator-v2/internal/timestamp"
)

// FileName is the backlog's own marks file, in the folder's root.
const FileName = "backlog.json"

// v1FileName is the v1 Sermon Marker's states file, read when a folder has
// no backlog.json yet.
const v1FileName = "bulk_states.json"

var videoExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".m4v": true, ".avi": true, ".flv": true, ".ts": true,
}

// Entry is one recording's marks in backlog.json.
type Entry struct {
	Recording string    `json:"recording"` // relative to the folder, with forward slashes
	Start     *float64  `json:"start,omitempty"`
	End       *float64  `json:"end,omitempty"`
	Date      string    `json:"date,omitempty"`
	Series    string    `json:"series,omitempty"`
	Skip      bool      `json:"skip,omitempty"`
	Updated   time.Time `json:"updated"`
}

type file struct {
	Version    int     `json:"version"`
	Recordings []Entry `json:"recordings"`
}

// Summary describes a backlog folder.
type Summary struct {
	Dir     string `json:"dir"`
	Total   int    `json:"total"`
	Marked  int    `json:"marked"`
	Skipped int    `json:"skipped"`
	Missing bool   `json:"missing"` // the folder isn't there (another machine, or a removed drive)
}

// Scan lists the video files under dir, recursively, as sorted relative
// paths with forward slashes.
func Scan(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // skip unreadable subfolders
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if videoExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			rel, err := filepath.Rel(dir, path)
			if err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, err
}

// Load reads the folder's marks: backlog.json, else a v1 Sermon Marker
// bulk_states.json, else none.
func Load(dir string) ([]Entry, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err == nil {
		var f file
		if err := json.Unmarshal([]byte(strings.TrimPrefix(string(raw), "\ufeff")), &f); err != nil {
			return nil, fmt.Errorf("reading %s: %w", FileName, err)
		}
		return f.Recordings, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return loadV1(dir)
}

func loadV1(dir string) ([]Entry, error) {
	raw, err := os.ReadFile(filepath.Join(dir, v1FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states []struct {
		Recording string          `json:"recording_path"`
		Begin     json.RawMessage `json:"raw_begin_offset"`
		End       json.RawMessage `json:"raw_end_offset"`
		Stitch    struct {
			Series string `json:"series"`
			Output string `json:"output"`
		} `json:"stitch"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(raw), "\ufeff")), &states); err != nil {
		return nil, fmt.Errorf("reading %s: %w", v1FileName, err)
	}
	var out []Entry
	for _, st := range states {
		rel := filepath.ToSlash(strings.ReplaceAll(st.Recording, `\`, "/"))
		if rel == "" || filepath.IsAbs(rel) {
			continue
		}
		e := Entry{Recording: rel, Series: st.Stitch.Series, Date: jobs.DateFromName(st.Stitch.Output)}
		start, okS := offset(st.Begin)
		end, okE := offset(st.End)
		if okS && okE {
			e.Start, e.End = &start, &end
		}
		out = append(out, e)
	}
	return out, nil
}

func offset(raw json.RawMessage) (float64, bool) {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	v, err := timestamp.Parse(s)
	return v, err == nil
}

// Save writes the marks of the backlog's jobs to its backlog.json,
// replacing the file atomically.
func Save(dir string, js []*jobs.Job) error {
	f := file{Version: 1, Recordings: []Entry{}}
	for _, j := range js {
		if j.Backlog != dir {
			continue
		}
		rel, err := filepath.Rel(dir, j.Recording)
		if err != nil {
			continue
		}
		f.Recordings = append(f.Recordings, Entry{
			Recording: filepath.ToSlash(rel), Start: j.Start, End: j.End,
			Date: j.Date, Series: j.Series, Skip: j.Skipped, Updated: j.Updated,
		})
	}
	sort.Slice(f.Recordings, func(a, b int) bool {
		return strings.ToLower(f.Recordings[a].Recording) < strings.ToLower(f.Recordings[b].Recording)
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".backlog-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, FileName))
}

// Sync brings the store in line with the folder: a job for every
// recording, with marks from backlog.json where they are newer than the
// job's. Only marked jobs are written back, so a fresh folder isn't
// touched until something is marked.
func Sync(st *store.Store, dir string) (Summary, error) {
	dir = filepath.Clean(dir)
	sum := Summary{Dir: dir}
	if _, err := os.Stat(dir); err != nil {
		sum.Missing = true
		return sum, nil
	}
	files, err := Scan(dir)
	if err != nil {
		return sum, err
	}
	entries, err := Load(dir)
	if err != nil {
		return sum, err
	}
	byRel := map[string]Entry{}
	for _, e := range entries {
		byRel[strings.ToLower(e.Recording)] = e
	}
	set, err := st.Settings()
	if err != nil {
		return sum, err
	}
	all, err := st.Jobs()
	if err != nil {
		return sum, err
	}
	existing := map[string]*jobs.Job{}
	for _, j := range all {
		if j.Backlog == dir {
			if rel, err := filepath.Rel(dir, j.Recording); err == nil {
				existing[strings.ToLower(filepath.ToSlash(rel))] = j
			}
		}
	}
	for _, rel := range files {
		key := strings.ToLower(rel)
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		e, hasEntry := byRel[key]
		j, ok := existing[key]
		changed := false
		if !ok {
			j = jobs.New(jobs.SourceBacklog, set)
			j.Backlog, j.Recording = dir, abs
			j.Date = jobs.DateFromName(rel)
			if j.Date == "" {
				if info, err := os.Stat(abs); err == nil {
					j.Date = info.ModTime().Format(time.DateOnly)
				}
			}
			changed = true
		}
		if hasEntry && (!ok || e.Updated.After(j.Updated)) {
			apply(j, e)
			changed = true
		}
		if changed {
			if err := st.SaveJob(j); err != nil {
				return sum, err
			}
		}
		sum.Total++
		switch {
		case j.Skipped:
			sum.Skipped++
		case j.Start != nil && j.End != nil:
			sum.Marked++
		}
	}
	return sum, nil
}

func apply(j *jobs.Job, e Entry) {
	if e.Start != nil && e.End != nil && (j.Start == nil || *j.Start != *e.Start || *j.End != *e.End) {
		j.SetMarks(*e.Start, *e.End)
	}
	if e.Date != "" {
		j.Date = e.Date
	}
	if e.Series != "" {
		j.Series = e.Series
	}
	j.Skipped = e.Skip
	j.Status = j.BaseStatus()
}

// SaveFor rewrites dir's backlog.json from the store's jobs.
func SaveFor(st *store.Store, dir string) error {
	all, err := st.Jobs()
	if err != nil {
		return err
	}
	var marked []*jobs.Job
	for _, j := range all {
		if j.Backlog == dir && (j.Skipped || (j.Start != nil && j.End != nil)) {
			marked = append(marked, j)
		}
	}
	return Save(dir, marked)
}
