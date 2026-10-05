package jobs_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

func settings(t *testing.T) jobs.Settings {
	s := jobs.DefaultSettings()
	s.TrimmedDir, s.FinalDir = filepath.Join(t.TempDir(), "trimmed"), filepath.Join(t.TempDir(), "final")
	return s
}

func dated(s jobs.Settings, date string) *jobs.Job {
	j := jobs.New(jobs.SourceFile, s)
	j.Date = date
	return j
}

func TestAssignStem(t *testing.T) {
	s := settings(t)
	var all []*jobs.Job
	add := func(date string) *jobs.Job {
		j := dated(s, date)
		jobs.AssignStem(j, all, s)
		all = append(all, j)
		return j
	}
	a, b, c := add("2026-10-04"), add("2026-10-04"), add("2026-10-04")
	if a.Stem != "2026-10-04" || b.Stem != "2026-10-04_2" || c.Stem != "2026-10-04_3" {
		t.Fatalf("stems %q %q %q", a.Stem, b.Stem, c.Stem)
	}
	// Re-saving keeps a job's stem rather than renumbering it.
	jobs.AssignStem(b, all, s)
	if b.Stem != "2026-10-04_2" {
		t.Errorf("re-save changed stem to %q", b.Stem)
	}
	// Changing the date picks a stem for the new date.
	b.Date = "2026-10-11"
	jobs.AssignStem(b, all, s)
	if b.Stem != "2026-10-11" {
		t.Errorf("new date stem %q", b.Stem)
	}
	// No date: the ID.
	n := jobs.New(jobs.SourceFile, s)
	jobs.AssignStem(n, all, s)
	if n.Stem != n.ID {
		t.Errorf("undated stem %q, want the ID", n.Stem)
	}
	if !strings.HasSuffix(a.TrimPath(s), "2026-10-04_trimmed.mp4") || !strings.HasSuffix(a.FinalPath(s), "2026-10-04.mp4") {
		t.Errorf("paths %s %s", a.TrimPath(s), a.FinalPath(s))
	}
}

func TestAssignStemSkipsExistingFiles(t *testing.T) {
	s := settings(t)
	if err := os.MkdirAll(s.FinalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A video rendered earlier, by a job since deleted.
	if err := os.WriteFile(filepath.Join(s.FinalDir, "2026-10-04.mp4"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	j := dated(s, "2026-10-04")
	jobs.AssignStem(j, nil, s)
	if j.Stem != "2026-10-04_2" {
		t.Errorf("stem %q, want 2026-10-04_2", j.Stem)
	}
}

func TestStatusFollowsMarks(t *testing.T) {
	j := jobs.New(jobs.SourceFile, jobs.DefaultSettings())
	if j.Status != jobs.StatusDraft {
		t.Fatalf("new job is %s", j.Status)
	}
	j.Recording = "rec.mkv"
	j.SetMarks(10, 20)
	if j.Status != jobs.StatusReady {
		t.Errorf("marked job is %s", j.Status)
	}
	j.Trimmed, j.Status = "t.mp4", jobs.StatusTrimmed
	j.SetMarks(11, 20)
	if j.Trimmed != "" || j.Status != jobs.StatusReady {
		t.Errorf("re-marking kept the old trim: %+v", j)
	}
}

func TestParseDate(t *testing.T) {
	for d, want := range map[string]bool{"2026-10-04": true, "2026-02-30": false, "2026-1-4": false, "": false} {
		if jobs.ParseDate(d) != want {
			t.Errorf("ParseDate(%q) != %v", d, want)
		}
	}
}

func TestCheck(t *testing.T) {
	tools := testmedia.Tools(t)
	rec := testmedia.ColorBlocks(t, tools, 6)
	intro := testmedia.Make(t, tools, "intro.png", "-f", "lavfi", "-i", "color=c=white:s=160x120", "-frames:v", "1")
	s := settings(t)
	series := map[string]jobs.Series{
		"Fall":   {Name: "Fall", Intro: intro, Outro: intro, TransitionDuration: 1},
		"Broken": {Name: "Broken", Intro: intro, Outro: "/missing/outro.mp4", TransitionDuration: 1},
	}
	c := jobs.Checker{Tools: tools, Settings: s, Series: func(n string) (jobs.Series, bool) { sr, ok := series[n]; return sr, ok }}
	both := []jobs.Step{jobs.StepTrim, jobs.StepStitch}
	ctx := context.Background()

	good := jobs.New(jobs.SourceFile, s)
	good.Recording, good.Series = rec, "Fall"
	good.SetMarks(1, 5)
	if p := c.Check(ctx, good, both); len(p) != 0 {
		t.Errorf("good job has problems: %v", p)
	}

	cases := []struct {
		want  string
		steps []jobs.Step
		edit  func(*jobs.Job)
	}{
		{"No recording", both, func(j *jobs.Job) { j.Recording = "" }},
		{"Recording not found", both, func(j *jobs.Job) { j.Recording = "/missing.mkv" }},
		{"aren't both set", both, func(j *jobs.Job) { j.End = nil }},
		{"must be after", both, func(j *jobs.Job) { j.SetMarks(4, 2) }},
		{"past the end of the recording", both, func(j *jobs.Job) { j.SetMarks(1, 30) }},
		{"too short for the series", both, func(j *jobs.Job) { j.SetMarks(1, 1.5) }},
		{"No series", both, func(j *jobs.Job) { j.Series = "" }},
		{"doesn't exist", both, func(j *jobs.Job) { j.Series = "Nope" }},
		{"is missing", both, func(j *jobs.Job) { j.Series = "Broken" }},
		{"hasn't been trimmed", []jobs.Step{jobs.StepStitch}, func(j *jobs.Job) {}},
		{"trimmed clip is missing", []jobs.Step{jobs.StepStitch}, func(j *jobs.Job) { j.Trimmed = "/missing/t.mp4" }},
	}
	for _, tc := range cases {
		j := *good
		tc.edit(&j)
		p := c.Check(ctx, &j, tc.steps)
		if !strings.Contains(strings.Join(p, " | "), tc.want) {
			t.Errorf("%s: got %v", tc.want, p)
		}
	}

	// Trim alone doesn't need a series.
	j := *good
	j.Series = ""
	if p := c.Check(ctx, &j, []jobs.Step{jobs.StepTrim}); len(p) != 0 {
		t.Errorf("trim-only job needs no series: %v", p)
	}
}

func TestCheckBatchCatchesSharedOutputs(t *testing.T) {
	tools := testmedia.Tools(t)
	rec := testmedia.ColorBlocks(t, tools, 6)
	s := settings(t)
	c := jobs.Checker{Tools: tools, Settings: s, Series: func(string) (jobs.Series, bool) { return jobs.Series{}, false }}
	a, b := jobs.New(jobs.SourceFile, s), jobs.New(jobs.SourceFile, s)
	for _, j := range []*jobs.Job{a, b} {
		j.Recording = rec
		j.SetMarks(1, 5)
	}
	b.TrimOutput = strings.ToUpper(a.TrimPath(s)[:1]) + a.TrimPath(s)[1:] // same file, different case
	problems := c.CheckBatch(context.Background(), []*jobs.Job{a, b}, []jobs.Step{jobs.StepTrim})
	if len(problems[a.ID]) != 0 || !strings.Contains(strings.Join(problems[b.ID], ""), "also written by job "+a.ID) {
		t.Errorf("problems: %v", problems)
	}
}
