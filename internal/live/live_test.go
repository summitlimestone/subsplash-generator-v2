package live

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
)

// fakeOBS is an OBS whose recording the test controls.
type fakeOBS struct {
	mu        sync.Mutex
	recording bool
	elapsed   time.Duration
	dir       string
	events    chan RecordEvent
	closed    bool
}

func (f *fakeOBS) Events() <-chan RecordEvent { return f.events }
func (f *fakeOBS) RecordStatus() (RecordStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return RecordStatus{Active: f.recording, Elapsed: f.elapsed}, nil
}
func (f *fakeOBS) RecordDirectory() (string, error) { return f.dir, nil }
func (f *fakeOBS) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
}
func (f *fakeOBS) set(recording bool, elapsed time.Duration) {
	f.mu.Lock()
	f.recording, f.elapsed = recording, elapsed
	f.mu.Unlock()
}

// dropper hands out fakeOBS connections, recording each one.
type dropper struct {
	mu    sync.Mutex
	conns []*fakeOBS
	dir   string
	state func() (bool, time.Duration)
}

func (d *dropper) dial(string, int, string) (OBS, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f := &fakeOBS{events: make(chan RecordEvent, 4), dir: d.dir}
	if d.state != nil {
		f.recording, f.elapsed = d.state()
	}
	d.conns = append(d.conns, f)
	return f, nil
}

func (d *dropper) current() *fakeOBS {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.conns) == 0 {
		return nil
	}
	return d.conns[len(d.conns)-1]
}

type rig struct {
	m     *Manager
	st    *store.Store
	obs   *dropper
	slide func(uid, text string)
}

func setup(t *testing.T, padStart float64) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// A ProPresenter stage display that sends whatever slides the test shows.
	slides := make(chan [2]string, 8)
	pp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		_, auth, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var a map[string]any
		_ = json.Unmarshal(auth, &a)
		ok := a["pwd"] == "stage"
		ack, _ := json.Marshal(map[string]any{"acn": "ath", "ath": ok})
		_ = c.Write(r.Context(), websocket.MessageText, ack)
		if !ok {
			return
		}
		for s := range slides {
			msg, _ := json.Marshal(map[string]any{"acn": "fv", "ary": []map[string]string{
				{"acn": "cs", "uid": s[0], "txt": s[1]}, {"acn": "ns", "uid": "next", "txt": "next"},
			}})
			if c.Write(r.Context(), websocket.MessageText, msg) != nil {
				return
			}
		}
	}))
	t.Cleanup(pp.Close)
	host, portStr, _ := net.SplitHostPort(pp.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	set := jobs.DefaultSettings()
	set.OBS.Host, set.OBS.Port = "obs", 4455
	set.ProPresenter.Host, set.ProPresenter.Port, set.ProPresenter.Password = host, port, "stage"
	set.ProPresenter.BeginSlide = jobs.Slide{UID: "BEGIN"}
	set.ProPresenter.EndSlide = jobs.Slide{Text: "amen", Match: "exact"}
	set.PadStart = padStart
	if err := st.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	r := &rig{st: st, obs: &dropper{dir: t.TempDir()}}
	r.m = &Manager{Store: st, Dial: r.obs.dial}
	r.slide = func(uid, text string) { slides <- [2]string{uid, text} }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.m.Run(ctx)
	r.waitFor(t, "connections", func(s State) bool { return s.OBSConnected && s.PPConnected })
	return r
}

func (r *rig) waitFor(t *testing.T, what string, cond func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := r.m.State()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; state %+v", what, s)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *rig) job(t *testing.T) *jobs.Job {
	t.Helper()
	j, err := r.st.Job(r.m.State().JobID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestServiceWithSlideMarks(t *testing.T) {
	r := setup(t, 0.6)
	if err := r.m.Start("Fall"); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Mark("start"); !errors.Is(err, ErrNotApplicable) {
		t.Errorf("marking before recording: %v", err)
	}
	obs := r.obs.current()
	obs.set(true, 0)
	obs.events <- RecordEvent{Recording: true}
	r.waitFor(t, "recording", func(s State) bool { return s.Phase == PhaseRecording && s.JobID != "" })
	if j := r.job(t); j.Source != jobs.SourceLive || j.Series != "Fall" || j.Date != time.Now().Format(time.DateOnly) {
		t.Errorf("live job %+v", j)
	}

	// The end slide before the begin slide does nothing.
	r.slide("x", "Amen")
	obs.set(true, 125*time.Second)
	r.slide("BEGIN", "")
	r.waitFor(t, "start mark", func(State) bool { return r.job(t).Start != nil })
	if got := *r.job(t).Start; !near(got, 125.6) {
		t.Errorf("start %v, want 125.6 (125 s + 0.6 s padding)", got)
	}
	obs.set(true, 2400*time.Second)
	r.slide("y", "AMEN")
	r.waitFor(t, "end mark", func(State) bool { return r.job(t).End != nil })
	if got := *r.job(t).End; !near(got, 2400) {
		t.Errorf("end %v", got)
	}
	// A later begin slide doesn't move a start that's set.
	obs.set(true, 2500*time.Second)
	r.slide("BEGIN", "")
	r.waitFor(t, "slide list", func(s State) bool { return len(s.Slides) == 4 })
	if got := *r.job(t).Start; !near(got, 125.6) {
		t.Errorf("start moved to %v", got)
	}

	// Nudges, and their limits.
	before := *r.job(t).Start
	if err := r.m.Nudge("start", -0.6); err != nil || !near(*r.job(t).Start, before-0.6) {
		t.Errorf("nudge: %v, start %v", err, *r.job(t).Start)
	}
	if err := r.m.Nudge("start", 5000); !errors.Is(err, ErrNotApplicable) {
		t.Errorf("nudging start past end: %v", err)
	}

	file := filepath.Join(r.obs.dir, "2026-10-05 10-00-00.mkv")
	obs.set(false, 0)
	obs.events <- RecordEvent{Recording: false, Path: file}
	r.waitFor(t, "stopped", func(s State) bool { return s.Phase == PhaseStopped })
	if j := r.job(t); j.Recording != file || j.Status != jobs.StatusReady {
		t.Errorf("finished job %+v", j)
	}
	r.m.Stop()
	if s := r.m.State(); s.Watching || s.JobID == "" {
		t.Errorf("after stop, the finished job stays shown: %+v", s)
	}
}

func TestManualMarksAndRecordingAlreadyRunning(t *testing.T) {
	r := setup(t, 0)
	obs := r.obs.current()
	obs.set(true, 300*time.Second)
	// Watching starts mid-recording: the job starts right away.
	_ = r.m.Start("")
	r.waitFor(t, "recording", func(s State) bool { return s.Phase == PhaseRecording })
	if err := r.m.Mark("end"); !errors.Is(err, ErrNotApplicable) {
		t.Errorf("end before start: %v", err)
	}
	if err := r.m.Mark("start"); err != nil {
		t.Fatal(err)
	}
	obs.set(true, 310*time.Second)
	if err := r.m.Mark("end"); err != nil {
		t.Fatal(err)
	}
	// Marking again moves the mark.
	obs.set(true, 320*time.Second)
	_ = r.m.Mark("end")
	if j := r.job(t); !near(*j.Start, 300) || !near(*j.End, 320) {
		t.Errorf("marks %v %v", *j.Start, *j.End)
	}
	_ = r.m.SetSeries("Advent")
	if r.job(t).Series != "Advent" {
		t.Error("series not applied to the job")
	}
}

func TestOBSDropWhileRecording(t *testing.T) {
	r := setup(t, 0)
	obs := r.obs.current()
	obs.set(true, 60*time.Second)
	_ = r.m.Start("")
	r.waitFor(t, "recording", func(s State) bool { return s.Phase == PhaseRecording })
	_ = r.m.Mark("start")

	// OBS drops, and the recording ends while disconnected. On reconnect
	// the file is found in OBS's recording folder.
	file := filepath.Join(r.obs.dir, "service.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.obs.state = func() (bool, time.Duration) { return false, 0 }
	obs.Close()
	r.waitFor(t, "notice", func(s State) bool { return !s.OBSConnected && s.Notice != "" })
	r.waitFor(t, "reconnected and stopped", func(s State) bool { return s.OBSConnected && s.Phase == PhaseStopped })
	if j := r.job(t); j.Recording != file || !near(*j.Start, 60) {
		t.Errorf("job after the drop %+v", j)
	}
}

func TestProPresenterWrongPassword(t *testing.T) {
	r := setup(t, 0)
	set, _ := r.st.Settings()
	set.ProPresenter.Password = "wrong"
	_ = r.st.SaveSettings(set)
	r.m.Reconfigure()
	r.waitFor(t, "auth error", func(s State) bool { return !s.PPConnected && s.PPError != "" })
	if s := r.m.State(); s.PPError != "ProPresenter rejected the password (Stage App password)" {
		t.Errorf("error %q", s.PPError)
	}
}

// near allows for the few milliseconds the test itself takes.
func near(a, b float64) bool { return a > b-0.05 && a < b+0.05 }

func TestMarkCorrectsForASlowOBS(t *testing.T) {
	r := setup(t, 0)
	obs := r.obs.current()
	obs.set(true, 100*time.Second)
	_ = r.m.Start("")
	r.waitFor(t, "recording", func(s State) bool { return s.Phase == PhaseRecording })
	// The button was pressed 3 s ago; OBS answers now with 100 s.
	if err := r.m.markAt("start", time.Now().Add(-3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := *r.job(t).Start; !near(got, 97) {
		t.Errorf("start %v, want 97", got)
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		cfg  jobs.Slide
		s    Slide
		want bool
	}{
		{jobs.Slide{UID: "ABC"}, Slide{UID: "abc"}, true},
		{jobs.Slide{UID: "ABC"}, Slide{UID: "abd", Text: "ABC"}, false},
		{jobs.Slide{Text: "Amen"}, Slide{Text: "amen"}, true},
		{jobs.Slide{Text: "Amen", CaseSensitive: true}, Slide{Text: "amen"}, false},
		{jobs.Slide{Text: "Amen"}, Slide{Text: "Amen!"}, false},
		{jobs.Slide{Text: `^Sermon:`, Match: "regex"}, Slide{Text: "sermon: Grace"}, true},
		{jobs.Slide{Text: `[`, Match: "regex"}, Slide{Text: "["}, false},
		{jobs.Slide{}, Slide{}, false},
	}
	for _, c := range cases {
		if got := Matches(c.cfg, c.s); got != c.want {
			t.Errorf("Matches(%+v, %+v) = %v", c.cfg, c.s, got)
		}
	}
}
