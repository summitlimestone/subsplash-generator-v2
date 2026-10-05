package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

type fixture struct {
	st    *store.Store
	tools ffmpeg.Tools
	set   jobs.Settings
	rec   string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	tools := testmedia.Tools(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	set := jobs.DefaultSettings()
	set.TrimmedDir, set.FinalDir = filepath.Join(t.TempDir(), "trimmed"), filepath.Join(t.TempDir(), "final")
	set.Render = jobs.Render{TrimCRF: 30, StitchCRF: 30, Encoder: "software", Preset: "ultrafast", FastCopy: true}
	if err := st.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	intro := testmedia.Make(t, tools, "intro.png", "-f", "lavfi", "-i", "color=c=white:s=160x120", "-frames:v", "1")
	if err := st.SaveSeries(jobs.Series{Name: "Fall", Intro: intro, IntroDuration: 1, Outro: intro, OutroDuration: 1,
		Transition: "fade", TransitionDuration: 0.5}); err != nil {
		t.Fatal(err)
	}
	return &fixture{st: st, tools: tools, set: set, rec: testmedia.ColorBlocks(t, tools, 6)}
}

func (f *fixture) job(t *testing.T, date string, source jobs.Source) *jobs.Job {
	t.Helper()
	j := jobs.New(source, f.set)
	j.Recording, j.Date, j.Series = f.rec, date, "Fall"
	j.SetMarks(1.2, 4.8)
	if err := f.st.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	return j
}

// events records every event and lets tests wait for a condition.
type events struct {
	mu  sync.Mutex
	all []Event
}

func (e *events) add(ev Event) { e.mu.Lock(); e.all = append(e.all, ev); e.mu.Unlock() }

func (e *events) statuses(id string) []jobs.Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []jobs.Status
	for _, ev := range e.all {
		if ev.Job.ID == id && ev.Progress == nil && (len(out) == 0 || out[len(out)-1] != ev.Job.Status) {
			out = append(out, ev.Job.Status)
		}
	}
	return out
}

func run(t *testing.T, f *fixture) (*Queue, *events, context.CancelFunc) {
	ev := &events{}
	q := New(f.st, f.tools, nil, ev.add)
	ctx, cancel := context.WithCancel(context.Background())
	go q.Run(ctx)
	t.Cleanup(cancel)
	return q, ev, cancel
}

func TestTrimAndStitch(t *testing.T) {
	f := setup(t)
	q, ev, _ := run(t, f)
	a, b := f.job(t, "2026-10-04", jobs.SourceFile), f.job(t, "2026-10-04", jobs.SourceFile)
	if err := q.Add(context.Background(), []string{a.ID, b.ID}, []jobs.Step{jobs.StepStitch, jobs.StepTrim}); err != nil {
		t.Fatal(err)
	}
	q.Wait()
	for i, j := range []*jobs.Job{a, b} {
		got, _ := f.st.Job(j.ID)
		if got.Status != jobs.StatusDone || got.Error != "" {
			t.Fatalf("job %d: %s %s", i, got.Status, got.Error)
		}
		info, err := media.Probe(context.Background(), f.tools, got.FinalPath(f.set))
		if err != nil || info.Duration < 4.45 || info.Duration > 4.75 { // 1 + 3.6 + 1 - 2*0.5
			t.Errorf("job %d final: %+v %v", i, info, err)
		}
	}
	want := []jobs.Status{jobs.StatusQueued, jobs.StatusTrimming, jobs.StatusTrimmed, jobs.StatusStitching, jobs.StatusDone}
	if got := ev.statuses(a.ID); strings.Join(toStrings(got), ",") != strings.Join(toStrings(want), ",") {
		t.Errorf("statuses %v, want %v", got, want)
	}
	if fin, _ := f.st.Job(b.ID); filepath.Base(fin.FinalPath(f.set)) != "2026-10-04_2.mp4" {
		t.Errorf("second job's output %s", fin.FinalPath(f.set))
	}
}

func toStrings(s []jobs.Status) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(v)
	}
	return out
}

func TestTrimOnlyRestsAtTrimmed(t *testing.T) {
	f := setup(t)
	q, _, _ := run(t, f)
	j := f.job(t, "2026-10-04", jobs.SourceFile)
	if err := q.Add(context.Background(), []string{j.ID}, []jobs.Step{jobs.StepTrim}); err != nil {
		t.Fatal(err)
	}
	q.Wait()
	got, _ := f.st.Job(j.ID)
	if got.Status != jobs.StatusTrimmed || got.Trimmed != got.TrimPath(f.set) {
		t.Fatalf("after trim: %s %q", got.Status, got.Trimmed)
	}
	if _, err := os.Stat(got.Trimmed); err != nil {
		t.Fatal(err)
	}
	// The stitch, later, from the checked trim.
	if err := q.Add(context.Background(), []string{j.ID}, []jobs.Step{jobs.StepStitch}); err != nil {
		t.Fatal(err)
	}
	q.Wait()
	if got, _ := f.st.Job(j.ID); got.Status != jobs.StatusDone {
		t.Errorf("after stitch: %s %s", got.Status, got.Error)
	}
}

func TestInvalidJobQueuesNothing(t *testing.T) {
	f := setup(t)
	q, _, _ := run(t, f)
	good, bad := f.job(t, "2026-10-04", jobs.SourceFile), f.job(t, "2026-10-05", jobs.SourceFile)
	if _, err := f.st.UpdateJob(bad.ID, func(j *jobs.Job) { j.Series = "Missing" }); err != nil {
		t.Fatal(err)
	}
	err := q.Add(context.Background(), []string{good.ID, bad.ID}, []jobs.Step{jobs.StepTrim, jobs.StepStitch})
	var ve ValidationError
	if !errors.As(err, &ve) || len(ve[bad.ID]) == 0 || len(ve[good.ID]) != 0 {
		t.Fatalf("got %v", err)
	}
	if got, _ := f.st.Job(good.ID); got.Status != jobs.StatusReady {
		t.Errorf("good job became %s", got.Status)
	}
}

func TestFailureDoesNotStopTheQueue(t *testing.T) {
	f := setup(t)
	q, _, _ := run(t, f)
	a, b := f.job(t, "2026-10-04", jobs.SourceFile), f.job(t, "2026-10-05", jobs.SourceFile)
	if err := q.Add(context.Background(), []string{a.ID, b.ID}, []jobs.Step{jobs.StepTrim, jobs.StepStitch}); err != nil {
		t.Fatal(err)
	}
	// Break a's series after validation, so its stitch fails mid-run.
	sr, _ := f.st.FindSeries("Fall")
	broken := sr
	broken.Name, broken.Outro = "Broken", "/missing/outro.mp4"
	_ = f.st.SaveSeries(broken)
	_, _ = f.st.UpdateJob(a.ID, func(j *jobs.Job) { j.Series = "Broken" })
	q.Wait()
	ga, _ := f.st.Job(a.ID)
	gb, _ := f.st.Job(b.ID)
	if ga.Status != jobs.StatusFailed || ga.Error == "" || ga.Trimmed == "" {
		t.Errorf("a: %s %q trimmed=%q", ga.Status, ga.Error, ga.Trimmed)
	}
	if gb.Status != jobs.StatusDone {
		t.Errorf("b: %s %s", gb.Status, gb.Error)
	}
}

func TestCancelRunningJob(t *testing.T) {
	f := setup(t)
	long := testmedia.Make(t, f.tools, "long.mp4", "-f", "lavfi", "-i", "testsrc2=s=640x360:r=30:d=60",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
	set := f.set
	set.Render.FastCopy, set.Render.Preset, set.Render.TrimCRF = false, "veryslow", 10
	_ = f.st.SaveSettings(set)
	j := jobs.New(jobs.SourceFile, set)
	j.Recording = long
	j.SetMarks(0, 59)
	_ = f.st.SaveJob(j)

	started := make(chan struct{})
	var once sync.Once
	q := New(f.st, f.tools, nil, func(e Event) {
		if e.Progress != nil {
			once.Do(func() { close(started) })
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	if err := q.Add(ctx, []string{j.ID}, []jobs.Step{jobs.StepTrim}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("never started")
	}
	q.Cancel(j.ID)
	q.Wait()
	got, _ := f.st.Job(j.ID)
	if got.Status != jobs.StatusReady || got.Error != "cancelled" {
		t.Errorf("after cancel: %s %q", got.Status, got.Error)
	}
	if _, err := os.Stat(got.TrimPath(set)); !os.IsNotExist(err) {
		t.Error("partial trim left behind")
	}
}

func TestLiveJobsGoFirst(t *testing.T) {
	f := setup(t)
	q := New(f.st, f.tools, nil, nil) // not running, so the order can be inspected
	a := f.job(t, "2026-10-01", jobs.SourceImport)
	b := f.job(t, "2026-10-02", jobs.SourceImport)
	live := f.job(t, "2026-10-04", jobs.SourceLive)
	ctx := context.Background()
	for _, id := range []string{a.ID, b.ID, live.ID} {
		if err := q.Add(ctx, []string{id}, []jobs.Step{jobs.StepTrim}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Add(ctx, []string{a.ID}, []jobs.Step{jobs.StepTrim}); err == nil {
		t.Error("queued the same job twice")
	}
	var order []string
	for _, it := range q.pending {
		order = append(order, it.jobID)
	}
	if want := []string{live.ID, a.ID, b.ID}; strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order %v, want %v", order, want)
	}
	q.Cancel(b.ID)
	if got, _ := f.st.Job(b.ID); got.Status != jobs.StatusReady || len(q.pending) != 2 {
		t.Errorf("cancelled queued job is %s; %d pending", got.Status, len(q.pending))
	}
}
