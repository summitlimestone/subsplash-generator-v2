package live

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
)

// Phase is where a live session is.
type Phase string

const (
	PhaseIdle      Phase = "idle"      // not watching
	PhaseWaiting   Phase = "waiting"   // watching for OBS to start recording
	PhaseRecording Phase = "recording" // recording; marks can be set
	PhaseStopped   Phase = "stopped"   // the recording finished; trim and stitch next
)

// State is everything the Live view shows.
type State struct {
	OBSConfigured bool    `json:"obsConfigured"`
	OBSConnected  bool    `json:"obsConnected"`
	OBSError      string  `json:"obsError"`
	Recording     bool    `json:"recording"`
	Elapsed       float64 `json:"elapsed"` // seconds into the current recording
	PPConfigured  bool    `json:"ppConfigured"`
	PPConnected   bool    `json:"ppConnected"`
	PPError       string  `json:"ppError"`
	Slides        []Slide `json:"slides"` // most recent first
	Watching      bool    `json:"watching"`
	Phase         Phase   `json:"phase"`
	JobID         string  `json:"jobId"`
	Series        string  `json:"series"`
	Notice        string  `json:"notice"`
}

const recentSlides = 20

// Manager keeps OBS and ProPresenter connected and runs live sessions.
type Manager struct {
	Store    *store.Store
	Dial     DialOBS
	OnChange func(State)      // called with every change, outside the lock
	OnJob    func(*jobs.Job)  // called when the session changes its job
	Now      func() time.Time // for tests; nil means time.Now

	mu           sync.Mutex
	st           State
	obs          OBS
	sessionStart time.Time
	ctx          context.Context
	stopConns    context.CancelFunc
}

// ErrNotApplicable is returned for an action that doesn't fit the
// session's phase, like marking before recording starts.
var ErrNotApplicable = errors.New("not possible right now")

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Run connects using the saved settings and keeps going until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.st.Phase = PhaseIdle
	m.mu.Unlock()
	m.Reconfigure()
	<-ctx.Done()
}

// Reconfigure reconnects with the current settings, e.g. after they change.
func (m *Manager) Reconfigure() {
	set, err := m.Store.Settings()
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.ctx == nil {
		m.mu.Unlock()
		return
	}
	if m.stopConns != nil {
		m.stopConns()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.stopConns = cancel
	m.st.OBSConfigured = set.OBS.Host != ""
	m.st.PPConfigured = set.ProPresenter.Host != ""
	m.st.OBSConnected, m.st.OBSError, m.st.PPConnected, m.st.PPError = false, "", false, ""
	m.mu.Unlock()
	m.changed()

	if set.OBS.Host != "" {
		go m.obsLoop(ctx, set.OBS.Host, set.OBS.Port, set.OBS.Password)
	}
	if pp := set.ProPresenter; pp.Host != "" {
		go runProPresenter(ctx, pp.Host, pp.Port, pp.Password, m.slide, func(ok bool, err error) {
			m.mu.Lock()
			m.st.PPConnected, m.st.PPError = ok, errText(err)
			m.mu.Unlock()
			m.changed()
		})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// State returns the current state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot()
}

func (m *Manager) snapshot() State {
	s := m.st
	s.Slides = slices.Clone(m.st.Slides)
	return s
}

func (m *Manager) changed() {
	if m.OnChange != nil {
		m.OnChange(m.State())
	}
}

func (m *Manager) obsLoop(ctx context.Context, host string, port int, password string) {
	for ctx.Err() == nil {
		conn, err := m.Dial(host, port, password)
		if err != nil {
			m.mu.Lock()
			m.st.OBSConnected, m.st.OBSError = false, err.Error()
			m.mu.Unlock()
			m.changed()
			if !sleep(ctx, reconnectInterval) {
				return
			}
			continue
		}
		m.mu.Lock()
		m.obs = conn
		m.st.OBSConnected, m.st.OBSError = true, ""
		m.mu.Unlock()
		m.reconcile()
		m.follow(ctx, conn)
		conn.Close()
		m.mu.Lock()
		m.obs = nil
		m.st.OBSConnected = false
		if ctx.Err() == nil {
			m.st.OBSError = "connection dropped; reconnecting"
			if m.st.Phase == PhaseRecording {
				m.st.Notice = "Lost the connection to OBS. Reconnecting; the marks so far are kept."
			}
		}
		m.mu.Unlock()
		m.changed()
		if !sleep(ctx, reconnectInterval) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// follow handles OBS's events and polls its recording time until the
// connection drops or ctx is done.
func (m *Manager) follow(ctx context.Context, conn OBS) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-conn.Events():
			if !ok {
				return
			}
			if ev.Recording {
				m.recordingStarted()
			} else {
				m.recordingStopped(ev.Path)
			}
		case <-tick.C:
			st, err := conn.RecordStatus()
			if err != nil {
				continue
			}
			m.mu.Lock()
			changed := m.st.Recording != st.Active || st.Active
			m.st.Recording, m.st.Elapsed = st.Active, st.Elapsed.Seconds()
			m.mu.Unlock()
			if changed {
				m.changed()
			}
		}
	}
}

// reconcile catches up with OBS after (re)connecting: a recording may have
// started before watching began, or stopped while disconnected.
func (m *Manager) reconcile() {
	m.mu.Lock()
	conn := m.obs
	m.mu.Unlock()
	if conn == nil {
		return
	}
	st, err := conn.RecordStatus()
	if err != nil {
		return
	}
	m.mu.Lock()
	m.st.Recording, m.st.Elapsed = st.Active, st.Elapsed.Seconds()
	phase := m.st.Phase
	m.mu.Unlock()
	switch {
	case st.Active && phase == PhaseWaiting:
		m.recordingStarted()
	case !st.Active && phase == PhaseRecording:
		m.recordingStopped("")
	default:
		m.changed()
	}
}

func (m *Manager) recordingStarted() {
	m.mu.Lock()
	m.st.Recording = true
	if !m.st.Watching || m.st.Phase == PhaseRecording {
		m.mu.Unlock()
		m.changed()
		return
	}
	series := m.st.Series
	m.mu.Unlock()

	set, err := m.Store.Settings()
	if err != nil {
		return
	}
	j := jobs.New(jobs.SourceLive, set)
	j.Date, j.Series = m.now().Format(time.DateOnly), series
	if err := m.Store.SaveJob(j); err != nil {
		m.notice("Couldn't start the live job: " + err.Error())
		return
	}
	m.mu.Lock()
	m.st.JobID, m.st.Phase, m.st.Notice = j.ID, PhaseRecording, ""
	m.sessionStart = m.now()
	m.mu.Unlock()
	m.jobChanged(j)
	m.changed()
}

func (m *Manager) recordingStopped(path string) {
	m.mu.Lock()
	m.st.Recording = false
	if m.st.Phase != PhaseRecording {
		m.mu.Unlock()
		m.changed()
		return
	}
	id, conn, since := m.st.JobID, m.obs, m.sessionStart
	m.mu.Unlock()

	notice := ""
	if path == "" {
		// Stopped while disconnected: OBS no longer says which file it was.
		path = findRecording(conn, since)
		if path == "" {
			notice = "The recording stopped while OBS was disconnected and its file couldn't be found. Choose it in the editor."
		}
	}
	j, err := m.Store.UpdateJob(id, func(j *jobs.Job) {
		if path != "" {
			j.Recording = path
		}
		j.Status = j.BaseStatus()
	})
	if err == nil {
		m.jobChanged(j)
	}
	m.mu.Lock()
	m.st.Phase, m.st.Notice = PhaseStopped, notice
	m.mu.Unlock()
	m.changed()
}

var videoExtensions = map[string]bool{".mkv": true, ".mp4": true, ".mov": true, ".flv": true, ".ts": true, ".m4v": true}

// findRecording guesses the session's file: the newest video in OBS's
// recording folder written since the session began.
func findRecording(conn OBS, since time.Time) string {
	if conn == nil {
		return ""
	}
	dir, err := conn.RecordDirectory()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestTime := "", since.Add(-time.Minute)
	for _, e := range entries {
		if e.IsDir() || !videoExtensions[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().After(bestTime) {
			best, bestTime = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best
}

func (m *Manager) slide(s Slide) {
	m.mu.Lock()
	m.st.Slides = append([]Slide{s}, m.st.Slides...)
	if len(m.st.Slides) > recentSlides {
		m.st.Slides = m.st.Slides[:recentSlides]
	}
	watching := m.st.Phase == PhaseRecording
	m.mu.Unlock()
	m.changed()
	if !watching {
		return
	}
	set, err := m.Store.Settings()
	if err != nil {
		return
	}
	j, err := m.job()
	if err != nil {
		return
	}
	// Slides only set marks that aren't set yet; a manual mark wins.
	var err2 error
	switch {
	case j.Start == nil && Matches(set.ProPresenter.BeginSlide, s):
		err2 = m.markAt("start", s.At)
	case j.Start != nil && j.End == nil && Matches(set.ProPresenter.EndSlide, s):
		err2 = m.markAt("end", s.At)
	}
	if err2 != nil {
		m.notice("A slide matched, but marking failed: " + err2.Error())
	}
}

func (m *Manager) job() (*jobs.Job, error) {
	m.mu.Lock()
	id := m.st.JobID
	m.mu.Unlock()
	if id == "" {
		return nil, ErrNotApplicable
	}
	return m.Store.Job(id)
}

// Start begins watching: the next recording (or the current one, if OBS
// is already recording) becomes a live job.
func (m *Manager) Start(series string) error {
	m.mu.Lock()
	if m.st.Watching {
		m.mu.Unlock()
		return fmt.Errorf("%w: already watching", ErrNotApplicable)
	}
	m.st.Watching, m.st.Phase, m.st.Series, m.st.JobID, m.st.Notice = true, PhaseWaiting, series, "", ""
	m.mu.Unlock()
	m.reconcile()
	m.changed()
	return nil
}

// Stop ends watching. The job keeps its marks.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.st.Watching = false
	if m.st.Phase != PhaseStopped {
		m.st.Phase, m.st.JobID = PhaseIdle, ""
	}
	m.st.Notice = ""
	m.mu.Unlock()
	m.changed()
}

// SetSeries sets the series for the session and its job.
func (m *Manager) SetSeries(name string) error {
	m.mu.Lock()
	m.st.Series = name
	id := m.st.JobID
	m.mu.Unlock()
	if id != "" {
		j, err := m.Store.UpdateJob(id, func(j *jobs.Job) { j.Series = name })
		if err != nil {
			return err
		}
		m.jobChanged(j)
	}
	m.changed()
	return nil
}

// Mark sets the start or end at the recording's current position, plus
// the padding setting. Marking again moves the mark.
func (m *Manager) Mark(which string) error { return m.markAt(which, m.now()) }

// markAt marks the recording's position at the moment at, when the slide
// changed or the button was pressed. A busy OBS can take seconds to answer,
// so its answer is wound back by how long ago that moment was, taking OBS's
// reading as being from the middle of the round trip.
func (m *Manager) markAt(which string, at time.Time) error {
	m.mu.Lock()
	conn, phase, id := m.obs, m.st.Phase, m.st.JobID
	m.mu.Unlock()
	if phase != PhaseRecording || id == "" {
		return fmt.Errorf("%w: marks are set while recording", ErrNotApplicable)
	}
	if conn == nil {
		return fmt.Errorf("%w: OBS isn't connected", ErrNotApplicable)
	}
	asked := m.now()
	status, err := conn.RecordStatus()
	if err != nil {
		return err
	}
	answered := m.now()
	set, err := m.Store.Settings()
	if err != nil {
		return err
	}
	reading := asked.Add(answered.Sub(asked) / 2)
	t := status.Elapsed.Seconds()
	if !status.Paused && reading.After(at) {
		t -= reading.Sub(at).Seconds()
	}
	if which == "start" {
		t += set.PadStart
	} else {
		t += set.PadEnd
	}
	return m.setMark(id, which, math.Max(0, t))
}

// Nudge moves a mark by delta seconds.
func (m *Manager) Nudge(which string, delta float64) error {
	j, err := m.job()
	if err != nil {
		return err
	}
	cur := j.Start
	if which == "end" {
		cur = j.End
	}
	if cur == nil {
		return fmt.Errorf("%w: the %s isn't marked yet", ErrNotApplicable, which)
	}
	return m.setMark(j.ID, which, math.Max(0, *cur+delta))
}

func (m *Manager) setMark(id, which string, t float64) error {
	t = math.Round(t*1000) / 1000
	var problem error
	j, err := m.Store.UpdateJob(id, func(j *jobs.Job) {
		switch which {
		case "start":
			if j.End != nil && t >= *j.End {
				problem = fmt.Errorf("%w: the start must be before the end", ErrNotApplicable)
				return
			}
			j.Start = &t
		case "end":
			if j.Start == nil || t <= *j.Start {
				problem = fmt.Errorf("%w: mark the start first, and the end after it", ErrNotApplicable)
				return
			}
			j.End = &t
		default:
			problem = fmt.Errorf("unknown mark %q", which)
			return
		}
		j.Trimmed = ""
		j.Status = j.BaseStatus()
	})
	if err != nil {
		return err
	}
	if problem != nil {
		return problem
	}
	m.jobChanged(j)
	m.changed()
	return nil
}

func (m *Manager) notice(msg string) {
	m.mu.Lock()
	m.st.Notice = msg
	m.mu.Unlock()
	m.changed()
}

func (m *Manager) jobChanged(j *jobs.Job) {
	if m.OnJob != nil {
		m.OnJob(j)
	}
}
