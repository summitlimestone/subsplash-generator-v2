package backlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
)

func touch(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openStore(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func backlogJobs(t *testing.T, st *store.Store, dir string) map[string]*jobs.Job {
	t.Helper()
	all, err := st.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*jobs.Job{}
	for _, j := range all {
		if j.Backlog == dir {
			rel, _ := filepath.Rel(dir, j.Recording)
			out[filepath.ToSlash(rel)] = j
		}
	}
	return out
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"b.mkv", "A.MP4", "2024/0107.mkv", "notes.txt", ".hidden/x.mkv", "thumb.jpg"} {
		touch(t, dir, f)
	}
	got, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2024/0107.mkv", "A.MP4", "b.mkv"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Scan = %v, want %v", got, want)
	}
}

func TestSyncCreatesDatedDrafts(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "2026-09-27 09-58-01.mkv")
	touch(t, dir, "old/service.mp4")
	st := openStore(t)
	sum, err := Sync(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 2 || sum.Marked != 0 {
		t.Errorf("summary %+v", sum)
	}
	js := backlogJobs(t, st, dir)
	a, b := js["2026-09-27 09-58-01.mkv"], js["old/service.mp4"]
	if a == nil || a.Date != "2026-09-27" || a.Status != jobs.StatusDraft || a.Source != jobs.SourceBacklog {
		t.Errorf("dated job %+v", a)
	}
	if b == nil || b.Date != time.Now().Format(time.DateOnly) {
		t.Errorf("undated job falls back to the file's date: %+v", b)
	}
	// Syncing again changes nothing, and writes no backlog.json for an
	// untouched folder.
	if _, err := Sync(st, dir); err != nil {
		t.Fatal(err)
	}
	if len(backlogJobs(t, st, dir)) != 2 {
		t.Error("a second sync duplicated jobs")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Error("an unmarked backlog got a backlog.json")
	}
}

func TestMarksTravelWithTheFolder(t *testing.T) {
	// Marked on the laptop...
	laptop := filepath.Join(t.TempDir(), "Backlog")
	touch(t, laptop, "2024/0107.mkv")
	touch(t, laptop, "2024/0114.mkv")
	touch(t, laptop, "2024/0121.mkv")
	st := openStore(t)
	if _, err := Sync(st, laptop); err != nil {
		t.Fatal(err)
	}
	js := backlogJobs(t, st, laptop)
	a := js["2024/0107.mkv"]
	a.SetMarks(1864.25, 4360)
	a.Series, a.Date = "Fall", "2024-01-07"
	b := js["2024/0114.mkv"]
	b.Skipped = true
	for _, j := range []*jobs.Job{a, b} {
		if err := st.SaveJob(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveFor(st, laptop); err != nil {
		t.Fatal(err)
	}

	// ...copied to the render machine, a different path and a fresh store.
	render := filepath.Join(t.TempDir(), "Copied Backlog")
	if err := os.CopyFS(render, os.DirFS(laptop)); err != nil {
		t.Fatal(err)
	}
	st2 := openStore(t)
	sum, err := Sync(st2, render)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 3 || sum.Marked != 1 || sum.Skipped != 1 {
		t.Errorf("summary on the render machine %+v", sum)
	}
	got := backlogJobs(t, st2, render)
	ga := got["2024/0107.mkv"]
	if ga == nil || *ga.Start != 1864.25 || *ga.End != 4360 || ga.Series != "Fall" || ga.Status != jobs.StatusReady ||
		ga.Recording != filepath.Join(render, "2024", "0107.mkv") {
		t.Errorf("marked job on the render machine %+v", ga)
	}
	if !got["2024/0114.mkv"].Skipped || got["2024/0121.mkv"].Start != nil {
		t.Error("skip or unmarked state didn't travel")
	}
}

func TestNewerMarksWin(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.mkv")
	st := openStore(t)
	if _, err := Sync(st, dir); err != nil {
		t.Fatal(err)
	}
	j := backlogJobs(t, st, dir)["a.mkv"]
	j.SetMarks(10, 20)
	_ = st.SaveJob(j)

	// An older backlog.json (say, copied back from before) doesn't
	// overwrite newer marks in the store...
	start, end := 1.0, 2.0
	write := func(updated time.Time) {
		data, _ := json.Marshal(file{Version: 1, Recordings: []Entry{{Recording: "a.mkv", Start: &start, End: &end, Updated: updated}}})
		_ = os.WriteFile(filepath.Join(dir, FileName), data, 0o644)
	}
	write(time.Now().Add(-time.Hour))
	_, _ = Sync(st, dir)
	if got := backlogJobs(t, st, dir)["a.mkv"]; *got.Start != 10 {
		t.Errorf("older file overwrote the store: start %v", *got.Start)
	}
	// ...but a newer one (marked elsewhere since) does.
	write(time.Now().Add(time.Hour))
	_, _ = Sync(st, dir)
	if got := backlogJobs(t, st, dir)["a.mkv"]; *got.Start != 1 {
		t.Errorf("newer file ignored: start %v", *got.Start)
	}
}

func TestReadsV1SermonMarkerFolder(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "input/2024/0107.mkv")
	touch(t, dir, "input/2024/0114.mkv")
	if err := os.WriteFile(filepath.Join(dir, "bulk_states.json"), []byte(`[
	  {"recording_path": "input/2024/0107.mkv", "raw_begin_offset": "00:31:04.250", "raw_end_offset": "01:12:40.000",
	   "trimmed_path": null, "trim": {"output": "output/2024-01-07_trimmed.mp4"},
	   "stitch": {"series": "Fall", "output": "output/2024-01-07.mp4"}}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	sum, err := Sync(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 2 || sum.Marked != 1 {
		t.Errorf("summary %+v", sum)
	}
	j := backlogJobs(t, st, dir)["input/2024/0107.mkv"]
	if *j.Start != 1864.25 || *j.End != 4360 || j.Series != "Fall" || j.Date != "2024-01-07" {
		t.Errorf("v1 marks %+v", j)
	}
}

func TestMissingFolder(t *testing.T) {
	sum, err := Sync(openStore(t), filepath.Join(t.TempDir(), "gone"))
	if err != nil || !sum.Missing {
		t.Errorf("missing folder: %+v %v", sum, err)
	}
}
