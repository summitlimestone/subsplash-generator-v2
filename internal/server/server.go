// Package server is the app's HTTP API, its push events and the web UI.
// The desktop window, the OBS dock and scripts all go through it.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/backlog"
	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/live"
	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/mediacache"
	"github.com/summitlimestone/subsplash-generator-v2/internal/queue"
	"github.com/summitlimestone/subsplash-generator-v2/internal/render"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
	"github.com/summitlimestone/subsplash-generator-v2/internal/timestamp"
	"github.com/summitlimestone/subsplash-generator-v2/internal/v1import"
)

// Server holds everything the API needs.
type Server struct {
	Store *store.Store
	Queue *queue.Queue
	Tools ffmpeg.Tools
	Media *mediacache.Cache
	Token string
	UI    fs.FS // the built web UI; may be nil
	Log   *slog.Logger
	// OpenFile, when the app has a window, shows a native file picker and
	// returns the chosen path, or "" if cancelled.
	OpenFile func(title string, patterns []string) (string, error)
	// OpenFolder, when the app has a window, shows a native folder picker.
	OpenFolder func(title string) (string, error)
	// Live runs live sessions; nil without the app.
	Live *live.Manager
	// NewToken makes a fresh API token.
	NewToken func() string
	// Version is the app's version, shown in Settings.
	Version string
	// Notices is the path of the third-party license notices; may be "".
	Notices string

	hub *hub
}

const cookieName = "sg_token"

// Init must be called before Handler. It returns the function the queue
// should report events to.
func (s *Server) Init() func(queue.Event) {
	s.hub = newHub()
	if s.Log == nil {
		s.Log = slog.New(slog.DiscardHandler)
	}
	return func(e queue.Event) {
		if e.Progress != nil {
			s.hub.publish("progress", map[string]any{"id": e.Job.ID, "label": e.Progress.Label, "fraction": e.Progress.Fraction, "speed": e.Progress.Speed})
			return
		}
		s.hub.publish("job", e.Job)
	}
}

// PeaksUpdated tells clients a recording's waveform grew.
func (s *Server) PeaksUpdated(path string) {
	s.hub.publish("peaks", map[string]string{"recording": path})
}

// Handler returns the HTTP handler: the API under /api/ and the UI at /.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h func(http.ResponseWriter, *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := h(w, r)
			if err != nil {
				writeError(w, err)
				return
			}
			if v != nil {
				writeJSON(w, http.StatusOK, v)
			}
		})
	}
	mux.HandleFunc("GET /api/events", s.events)
	api("GET /api/info", s.info)
	api("GET /api/jobs", func(http.ResponseWriter, *http.Request) (any, error) { return s.Store.Jobs() })
	api("POST /api/jobs", s.createJob)
	api("GET /api/jobs/{id}", func(_ http.ResponseWriter, r *http.Request) (any, error) { return s.Store.Job(r.PathValue("id")) })
	api("PATCH /api/jobs/{id}", s.patchJob)
	api("POST /api/jobs/edit", s.editJobs)
	api("POST /api/import", s.importStates)
	api("DELETE /api/jobs/{id}", s.deleteJob)
	api("POST /api/render", s.render)
	api("POST /api/jobs/{id}/cancel", s.cancel)
	api("GET /api/series", func(http.ResponseWriter, *http.Request) (any, error) { return s.Store.Series() })
	api("GET /api/settings", s.settings)
	api("POST /api/dialog/open", s.openDialog)
	api("POST /api/dialog/folder", s.folderDialog)
	api("GET /api/backlogs", s.backlogs)
	api("POST /api/backlogs", s.openBacklog)
	api("POST /api/backlogs/forget", s.forgetBacklog)
	api("GET /api/jobs/{id}/probe", s.probe)
	mux.HandleFunc("GET /api/jobs/{id}/video", s.video)
	mux.HandleFunc("GET /api/jobs/{id}/peaks", s.peaks)
	mux.HandleFunc("GET /api/jobs/{id}/thumb", s.thumb)
	mux.HandleFunc("GET /api/notices", s.notices)
	s.liveRoutes(api)
	mux.HandleFunc("GET /", s.ui)
	return s.auth(mux)
}

// auth accepts the token as a Bearer header, a ?token= parameter (which
// also sets a cookie, so a window or dock opened with it stays signed in),
// or that cookie. The cookie is SameSite=Strict, so other sites can't
// make a browser send it.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("token"); t != "" && s.valid(t) {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			next.ServeHTTP(w, r)
			return
		}
		supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if c, err := r.Cookie(cookieName); err == nil && supplied == "" {
			supplied = c.Value
		}
		if !s.valid(supplied) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or incorrect token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) valid(t string) bool {
	return s.Token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.Token)) == 1
}

type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return httpError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func writeError(w http.ResponseWriter, err error) {
	status, body := http.StatusInternalServerError, map[string]any{"error": err.Error()}
	var he httpError
	var ve queue.ValidationError
	switch {
	case errors.As(err, &he):
		status = he.status
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case errors.As(err, &ve):
		status, body["problems"] = http.StatusConflict, ve
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest("invalid request body: %v", err)
	}
	return nil
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type encoderInfo struct {
	Name          string   `json:"name"`
	Presets       []string `json:"presets"`
	DefaultPreset string   `json:"defaultPreset"`
}

func encoders() []encoderInfo {
	var out []encoderInfo
	for _, name := range render.EncoderNames() {
		e := render.Encoders[name]
		out = append(out, encoderInfo{name, e.Presets, e.DefaultPreset})
	}
	return out
}

func (s *Server) info(http.ResponseWriter, *http.Request) (any, error) {
	return map[string]any{
		"peaksPerSecond": mediacache.PeaksPerSecond,
		"canOpenFiles":   s.OpenFile != nil,
		"encoders":       encoders(),
		"version":        s.Version,
		"hasNotices":     s.Notices != "",
		"v1Found":        v1Found(),
	}, nil
}

// v1Found reports whether v1's settings are where v1 kept them.
func v1Found() bool {
	_, err := os.Stat(filepath.Join(v1import.Dir(), "config.json"))
	return err == nil
}

func (s *Server) notices(w http.ResponseWriter, r *http.Request) {
	if s.Notices == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeFile(w, r, s.Notices)
}

// jobEdit is what a client may change on a job. Nil fields are unchanged.
type jobEdit struct {
	Recording *string      `json:"recording"`
	Start     *float64     `json:"start"`
	End       *float64     `json:"end"`
	Date      *string      `json:"date"`
	Series    *string      `json:"series"`
	Render    *jobs.Render `json:"render"`
	Skipped   *bool        `json:"skipped"`
}

func (e jobEdit) apply(j *jobs.Job) error {
	if e.Date != nil && *e.Date != "" && !jobs.ParseDate(*e.Date) {
		return badRequest("%q isn't a date (YYYY-MM-DD)", *e.Date)
	}
	if (e.Start == nil) != (e.End == nil) {
		return badRequest("set start and end together")
	}
	if e.Start != nil && (*e.Start < 0 || *e.End <= *e.Start) {
		return badRequest("the end (%s) must be after the start (%s)", timestamp.Format(*e.End), timestamp.Format(*e.Start))
	}
	if e.Recording != nil && *e.Recording != j.Recording {
		j.Recording, j.Trimmed = *e.Recording, ""
	}
	if e.Start != nil && (j.Start == nil || *j.Start != *e.Start || *j.End != *e.End) {
		j.SetMarks(*e.Start, *e.End)
	}
	if e.Date != nil {
		j.Date = *e.Date
	}
	if e.Series != nil {
		j.Series = *e.Series
	}
	if e.Render != nil {
		j.Render = *e.Render
	}
	if e.Skipped != nil {
		j.Skipped = *e.Skipped
	}
	switch j.Status {
	case jobs.StatusDraft, jobs.StatusReady, jobs.StatusTrimmed, jobs.StatusDone, jobs.StatusFailed:
		j.Status, j.Error = j.BaseStatus(), ""
	}
	return nil
}

func (s *Server) createJob(_ http.ResponseWriter, r *http.Request) (any, error) {
	var e jobEdit
	if err := decode(r, &e); err != nil {
		return nil, err
	}
	set, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	j := jobs.New(jobs.SourceFile, set)
	if err := e.apply(j); err != nil {
		return nil, err
	}
	if e.Date == nil && j.Recording != "" {
		j.Date = jobs.DateFromName(j.Recording)
	}
	if err := s.Store.SaveJob(j); err != nil {
		return nil, err
	}
	s.hub.publish("job", j)
	return j, nil
}

func (s *Server) patchJob(_ http.ResponseWriter, r *http.Request) (any, error) {
	var e jobEdit
	if err := decode(r, &e); err != nil {
		return nil, err
	}
	js, err := s.edit([]string{r.PathValue("id")}, e)
	if err != nil {
		return nil, err
	}
	return js[0], nil
}

// editJobs applies one edit to several jobs, e.g. setting their series.
func (s *Server) editJobs(_ http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		IDs  []string `json:"ids"`
		Edit jobEdit  `json:"edit"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if len(req.IDs) == 0 {
		return nil, badRequest("no jobs selected")
	}
	return s.edit(req.IDs, req.Edit)
}

// edit applies e to every job in ids, checking them all first so a
// problem with one changes none, then saves backlog folders' marks.
func (s *Server) edit(ids []string, e jobEdit) ([]*jobs.Job, error) {
	var js []*jobs.Job
	for _, id := range ids {
		j, err := s.Store.Job(id)
		if err != nil {
			return nil, err
		}
		switch j.Status {
		case jobs.StatusQueued, jobs.StatusTrimming, jobs.StatusStitching:
			return nil, httpError{http.StatusConflict, fmt.Sprintf("%s is rendering; cancel it first", j.Stem)}
		}
		if err := e.apply(j); err != nil {
			return nil, err
		}
		js = append(js, j)
	}
	dirs := map[string]bool{}
	for _, j := range js {
		if err := s.Store.SaveJob(j); err != nil {
			return nil, err
		}
		s.hub.publish("job", j)
		if j.Backlog != "" {
			dirs[j.Backlog] = true
		}
	}
	for dir := range dirs {
		if err := backlog.SaveFor(s.Store, dir); err != nil {
			return nil, fmt.Errorf("saved, but couldn't update the folder's %s: %w", backlog.FileName, err)
		}
	}
	return js, nil
}

// importStates adds the jobs in a v1 render-state or bulk states file.
func (s *Server) importStates(_ http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	set, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	js, err := v1import.States(req.Path, set)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	for _, j := range js {
		if err := s.Store.SaveJob(j); err != nil {
			return nil, err
		}
		s.hub.publish("job", j)
	}
	return map[string]int{"imported": len(js)}, nil
}

func (s *Server) deleteJob(_ http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	s.Queue.Cancel(id)
	if err := s.Store.DeleteJob(id); err != nil {
		return nil, err
	}
	s.hub.publish("deleted", map[string]string{"id": id})
	return map[string]bool{"ok": true}, nil
}

func (s *Server) render(_ http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		IDs   []string    `json:"ids"`
		Steps []jobs.Step `json:"steps"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	for _, st := range req.Steps {
		if st != jobs.StepTrim && st != jobs.StepStitch {
			return nil, badRequest("unknown step %q", st)
		}
	}
	if err := s.Queue.Add(r.Context(), req.IDs, req.Steps); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) cancel(_ http.ResponseWriter, r *http.Request) (any, error) {
	s.Queue.Cancel(r.PathValue("id"))
	return map[string]bool{"ok": true}, nil
}

func (s *Server) openDialog(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.OpenFile == nil {
		return nil, httpError{http.StatusNotImplemented, "file picking needs the desktop app"}
	}
	var req struct {
		Title    string   `json:"title"`
		Patterns []string `json:"patterns"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	path, err := s.OpenFile(req.Title, req.Patterns)
	if err != nil {
		return nil, err
	}
	return map[string]string{"path": path}, nil
}

func (s *Server) folderDialog(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.OpenFolder == nil {
		return nil, httpError{http.StatusNotImplemented, "folder picking needs the desktop app"}
	}
	var req struct {
		Title string `json:"title"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	path, err := s.OpenFolder(req.Title)
	if err != nil {
		return nil, err
	}
	return map[string]string{"path": path}, nil
}

// backlogs rescans every opened backlog folder, so files added since
// show up, and summarizes them.
func (s *Server) backlogs(http.ResponseWriter, *http.Request) (any, error) {
	set, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	out := []backlog.Summary{}
	for _, dir := range set.Backlogs {
		sum, err := backlog.Sync(s.Store, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		out = append(out, sum)
	}
	s.publishAll()
	return out, nil
}

func (s *Server) openBacklog(_ http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	dir := filepath.Clean(req.Dir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, badRequest("%s isn't a folder", req.Dir)
	}
	if _, err := s.Store.UpdateSettings(func(set *jobs.Settings) {
		for _, d := range set.Backlogs {
			if d == dir {
				return
			}
		}
		set.Backlogs = append(set.Backlogs, dir)
	}); err != nil {
		return nil, err
	}
	sum, err := backlog.Sync(s.Store, dir)
	if err != nil {
		return nil, err
	}
	s.publishAll()
	return sum, nil
}

// forgetBacklog drops a backlog folder from the app. Its marks stay in
// the folder's backlog.json, so opening it again restores them.
func (s *Server) forgetBacklog(_ http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if _, err := s.Store.UpdateSettings(func(set *jobs.Settings) {
		set.Backlogs = slices.DeleteFunc(set.Backlogs, func(d string) bool { return d == req.Dir })
	}); err != nil {
		return nil, err
	}
	all, err := s.Store.Jobs()
	if err != nil {
		return nil, err
	}
	for _, j := range all {
		if j.Backlog == req.Dir {
			s.Queue.Cancel(j.ID)
			if err := s.Store.DeleteJob(j.ID); err != nil {
				return nil, err
			}
			s.hub.publish("deleted", map[string]string{"id": j.ID})
		}
	}
	return map[string]bool{"ok": true}, nil
}

// publishAll tells clients to reload the job list.
func (s *Server) publishAll() { s.hub.publish("reload", map[string]bool{"jobs": true}) }

func (s *Server) recordingOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	j, err := s.Store.Job(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return "", false
	}
	if j.Recording == "" {
		writeError(w, httpError{http.StatusNotFound, "the job has no recording"})
		return "", false
	}
	return j.Recording, true
}

// video serves the job's recording, with Range support so the player can
// seek anywhere in a multi-gigabyte file without downloading it.
func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	path, ok := s.recordingOf(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("file") == "trimmed" {
		j, _ := s.Store.Job(r.PathValue("id"))
		if j == nil || j.Trimmed == "" {
			writeError(w, httpError{http.StatusNotFound, "the job hasn't been trimmed"})
			return
		}
		path = j.Trimmed
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, httpError{http.StatusNotFound, err.Error()})
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeError(w, err)
		return
	}
	ctype := "video/mp4"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv":
		ctype = "video/x-matroska"
	case ".mov":
		ctype = "video/quicktime"
	}
	w.Header().Set("Content-Type", ctype)
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (s *Server) peaks(w http.ResponseWriter, r *http.Request) {
	path, ok := s.recordingOf(w, r)
	if !ok {
		return
	}
	data, complete, err := s.Media.Peaks(path)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Peaks-Complete", strconv.FormatBool(complete))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) thumb(w http.ResponseWriter, r *http.Request) {
	path, ok := s.recordingOf(w, r)
	if !ok {
		return
	}
	t, err := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	if err != nil || t < 0 {
		writeError(w, badRequest("t must be a time in seconds"))
		return
	}
	h, _ := strconv.Atoi(r.URL.Query().Get("h"))
	if h <= 0 || h > 360 {
		h = 72
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	file, err := s.Media.Thumbnail(ctx, path, t, h)
	if err != nil {
		if r.Context().Err() == nil {
			writeError(w, err)
		}
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, file)
}

// ui serves the built web UI, falling back to index.html for app routes.
func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	if s.UI == nil {
		http.Error(w, "the web UI isn't built", http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(s.UI, name); err != nil {
		name = "index.html"
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.UI, name)
}

// probe reports the recording's duration and frame rate, which the
// editor needs for frame stepping.
func (s *Server) probe(w http.ResponseWriter, r *http.Request) (any, error) {
	path, ok := s.recordingOf(w, r)
	if !ok {
		return nil, nil
	}
	info, err := media.Probe(r.Context(), s.Tools, path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"duration": info.Duration, "fps": info.FPS, "width": info.Width, "height": info.Height}, nil
}
