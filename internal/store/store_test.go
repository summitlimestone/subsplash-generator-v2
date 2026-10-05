package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func TestSettingsDefaultAndSave(t *testing.T) {
	st, _ := open(t)
	s, err := st.Settings()
	if err != nil || s.OBS.Port != 4455 {
		t.Fatalf("defaults %+v %v", s.OBS, err)
	}
	s.OBS.Password = "pw"
	if err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Settings(); got.OBS.Password != "pw" {
		t.Errorf("saved settings not read back: %+v", got.OBS)
	}
}

func TestJobsPersistAcrossReopen(t *testing.T) {
	st, path := open(t)
	set, _ := st.Settings()
	a := jobs.New(jobs.SourceFile, set)
	a.Recording, a.Date, a.Series = "rec.mkv", "2026-10-04", "Fall"
	a.SetMarks(83.075, 2046.419)
	b := jobs.New(jobs.SourceLive, set)
	b.Date = "2026-10-04"
	for _, j := range []*jobs.Job{a, b} {
		if err := st.SaveJob(j); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	all, err := st2.Jobs()
	if err != nil || len(all) != 2 {
		t.Fatalf("got %d jobs, %v", len(all), err)
	}
	got := all[0]
	if got.ID != a.ID || got.Stem != "2026-10-04" || *got.Start != 83.075 || got.Status != jobs.StatusReady || got.Series != "Fall" {
		t.Errorf("first job %+v", got)
	}
	if all[1].Stem != "2026-10-04_2" {
		t.Errorf("same-date job stem %q", all[1].Stem)
	}
	if _, err := st2.Job("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing job: %v", err)
	}
	if err := st2.DeleteJob(a.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := st2.Jobs(); len(left) != 1 {
		t.Errorf("%d jobs after delete", len(left))
	}
}

func TestSeries(t *testing.T) {
	st, _ := open(t)
	for _, sr := range []jobs.Series{{Name: "Fall", Intro: "a"}, {Name: "Advent"}, {Name: "Fall", Intro: "b"}} {
		if err := st.SaveSeries(sr); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := st.Series()
	if len(list) != 2 || list[0].Name != "Advent" {
		t.Errorf("series %+v", list)
	}
	if sr, err := st.FindSeries("Fall"); err != nil || sr.Intro != "b" {
		t.Errorf("Fall = %+v, %v", sr, err)
	}
	if err := st.SaveSeries(jobs.Series{}); err == nil {
		t.Error("saved a series with no name")
	}
	_ = st.DeleteSeries("Fall")
	if _, err := st.FindSeries("Fall"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted series: %v", err)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	st, _ := open(t)
	set, _ := st.Settings()
	mk := func(status jobs.Status, trimmed string) *jobs.Job {
		j := jobs.New(jobs.SourceFile, set)
		j.Recording, j.Trimmed = "rec.mkv", ""
		j.SetMarks(1, 2)
		j.Trimmed, j.Status = trimmed, status
		if err := st.SaveJob(j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	trimming := mk(jobs.StatusTrimming, "")
	stitching := mk(jobs.StatusStitching, "t.mp4")
	queued := mk(jobs.StatusQueued, "")
	done := mk(jobs.StatusDone, "t.mp4")
	ids, err := st.RecoverInterrupted()
	if err != nil || len(ids) != 3 {
		t.Fatalf("recovered %v, %v", ids, err)
	}
	for j, want := range map[*jobs.Job]jobs.Status{
		trimming: jobs.StatusReady, stitching: jobs.StatusTrimmed, queued: jobs.StatusReady, done: jobs.StatusDone,
	} {
		got, _ := st.Job(j.ID)
		if got.Status != want {
			t.Errorf("%s became %s, want %s", j.Status, got.Status, want)
		}
		if (j.Status == jobs.StatusTrimming || j.Status == jobs.StatusStitching) && got.Error == "" {
			t.Errorf("%s job has no interruption note", j.Status)
		}
	}
}
