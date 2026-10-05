package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/backlog"
	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/mediacache"
	"github.com/summitlimestone/subsplash-generator-v2/internal/queue"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

const token = "test-token"

type env struct {
	*httptest.Server
	srv *Server
	rec string
}

func setup(t *testing.T) *env {
	t.Helper()
	tools := testmedia.Tools(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	set := jobs.DefaultSettings()
	set.TrimmedDir, set.FinalDir = t.TempDir(), t.TempDir()
	set.Render.Encoder, set.Render.Preset, set.Render.Normalize = "software", "ultrafast", false
	_ = st.SaveSettings(set)
	srv := &Server{Store: st, Tools: tools, Media: mediacache.New(t.TempDir(), tools, nil), Token: token,
		UI: fstest.MapFS{"index.html": {Data: []byte("<h1>ui</h1>")}, "assets/app.js": {Data: []byte("js")}}}
	onEvent := srv.Init()
	srv.Queue = queue.New(st, tools, nil, onEvent)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Queue.Run(ctx)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{Server: ts, srv: srv, rec: testmedia.ColorBlocks(t, tools, 6)}
}

func (e *env) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res, data
}

func TestAuth(t *testing.T) {
	e := setup(t)
	for _, path := range []string{"/api/jobs", "/", "/api/events"} {
		res, err := http.Get(e.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", path, res.StatusCode)
		}
	}
	res, _ := http.Get(e.URL + "/?token=wrong")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	// ?token= signs in and sets a cookie that works afterwards.
	jar := &cookieJar{}
	client := &http.Client{Jar: jar}
	res, err := client.Get(e.URL + "/?token=" + token)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("token login: %v %v", res.StatusCode, err)
	}
	res, _ = client.Get(e.URL + "/api/jobs")
	if res.StatusCode != http.StatusOK {
		t.Errorf("cookie request: %d", res.StatusCode)
	}
	if c := jar.cookies[0]; !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie %+v", c)
	}
}

type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) SetCookies(_ *urlType, c []*http.Cookie) { j.cookies = append(j.cookies, c...) }
func (j *cookieJar) Cookies(*urlType) []*http.Cookie         { return j.cookies }

func TestUIFallsBackToIndex(t *testing.T) {
	e := setup(t)
	for path, want := range map[string]string{"/": "<h1>ui</h1>", "/jobs/123": "<h1>ui</h1>", "/assets/app.js": "js"} {
		_, body := e.do(t, "GET", path, nil)
		if string(body) != want {
			t.Errorf("%s: %q", path, body)
		}
	}
}

func TestJobLifecycle(t *testing.T) {
	e := setup(t)
	events := e.listen(t)

	res, body := e.do(t, "POST", "/api/jobs", map[string]any{"recording": e.rec})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var j jobs.Job
	_ = json.Unmarshal(body, &j)
	if j.Status != jobs.StatusDraft || j.Recording != e.rec {
		t.Fatalf("created %+v", j)
	}

	edits := []struct {
		body string
		want int
	}{
		{`{"start": 1}`, http.StatusBadRequest},
		{`{"start": 4, "end": 2}`, http.StatusBadRequest},
		{`{"date": "2026-13-40"}`, http.StatusBadRequest},
		{`{"start": 1.2, "end": 4.8, "date": "2026-10-04"}`, http.StatusOK},
	}
	for _, ed := range edits {
		var v any
		_ = json.Unmarshal([]byte(ed.body), &v)
		if res, body := e.do(t, "PATCH", "/api/jobs/"+j.ID, v); res.StatusCode != ed.want {
			t.Errorf("PATCH %s: %d %s", ed.body, res.StatusCode, body)
		}
	}
	_, body = e.do(t, "GET", "/api/jobs/"+j.ID, nil)
	_ = json.Unmarshal(body, &j)
	if j.Status != jobs.StatusReady || j.Stem != "2026-10-04" || *j.Start != 1.2 {
		t.Fatalf("after edit %+v", j)
	}

	// Stitch can't run before a trim: validation problems come back as 409.
	res, body = e.do(t, "POST", "/api/render", map[string]any{"ids": []string{j.ID}, "steps": []string{"stitch"}})
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(body), "hasn't been trimmed") {
		t.Errorf("stitch first: %d %s", res.StatusCode, body)
	}
	if res, body := e.do(t, "POST", "/api/render", map[string]any{"ids": []string{j.ID}, "steps": []string{"trim"}}); res.StatusCode != http.StatusOK {
		t.Fatalf("trim: %d %s", res.StatusCode, body)
	}
	events.waitFor(t, func(kind string, data map[string]any) bool { return kind == "job" && data["status"] == "trimmed" })
	if !events.saw("progress") {
		t.Error("no progress events")
	}

	// The trimmed clip plays from its own URL.
	res, _ = e.do(t, "GET", "/api/jobs/"+j.ID+"/video?file=trimmed", nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "video/mp4" {
		t.Errorf("trimmed video: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	if res, _ := e.do(t, "DELETE", "/api/jobs/"+j.ID, nil); res.StatusCode != http.StatusOK {
		t.Errorf("delete: %d", res.StatusCode)
	}
	events.waitFor(t, func(kind string, data map[string]any) bool { return kind == "deleted" && data["id"] == j.ID })
	if res, _ := e.do(t, "GET", "/api/jobs/"+j.ID, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("deleted job: %d", res.StatusCode)
	}
}

func TestCreateTakesTheDateFromTheFileName(t *testing.T) {
	e := setup(t)
	_, body := e.do(t, "POST", "/api/jobs", map[string]any{"recording": `D:\video\2026-10-04 09-57-37.mkv`})
	var j jobs.Job
	_ = json.Unmarshal(body, &j)
	if j.Date != "2026-10-04" {
		t.Errorf("date %q", j.Date)
	}
}

func TestVideoSupportsRanges(t *testing.T) {
	e := setup(t)
	_, body := e.do(t, "POST", "/api/jobs", map[string]any{"recording": e.rec})
	var j jobs.Job
	_ = json.Unmarshal(body, &j)
	req, _ := http.NewRequest("GET", e.URL+"/api/jobs/"+j.ID+"/video", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Range", "bytes=100-199")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || len(data) != 100 {
		t.Errorf("range: %d, %d bytes", res.StatusCode, len(data))
	}

	res, body = e.do(t, "GET", "/api/jobs/"+j.ID+"/probe", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"fps":30`) {
		t.Errorf("probe: %d %s", res.StatusCode, body)
	}
	res, _ = e.do(t, "GET", "/api/jobs/"+j.ID+"/thumb?t=2&h=48", nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("thumb: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		res, body = e.do(t, "GET", "/api/jobs/"+j.ID+"/peaks", nil)
		if res.Header.Get("X-Peaks-Complete") == "true" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("peaks never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if want := 6 * mediacache.PeaksPerSecond; len(body) < want-2 || len(body) > want+2 {
		t.Errorf("%d peaks", len(body))
	}
}

func TestOpenDialogNeedsTheApp(t *testing.T) {
	e := setup(t)
	if res, _ := e.do(t, "POST", "/api/dialog/open", map[string]any{}); res.StatusCode != http.StatusNotImplemented {
		t.Errorf("without a window: %d", res.StatusCode)
	}
	e.srv.OpenFile = func(title string, patterns []string) (string, error) { return "C:/picked.mkv", nil }
	if _, body := e.do(t, "POST", "/api/dialog/open", map[string]any{"title": "x"}); !strings.Contains(string(body), "picked.mkv") {
		t.Errorf("with a window: %s", body)
	}
}

func TestSettingsHideSecrets(t *testing.T) {
	e := setup(t)
	set, _ := e.srv.Store.Settings()
	set.OBS.Password, set.API.Token = "obs-secret", "api-secret"
	_ = e.srv.Store.SaveSettings(set)
	_, body := e.do(t, "GET", "/api/settings", nil)
	if strings.Contains(string(body), "secret") {
		t.Errorf("settings leaked a secret: %s", body)
	}
}

// eventLog reads the SSE stream in the background.
type eventLog struct {
	ch   chan [2]any
	seen map[string]bool
}

func (e *env) listen(t *testing.T) *eventLog {
	req, _ := http.NewRequest("GET", e.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	l := &eventLog{ch: make(chan [2]any, 1000), seen: map[string]bool{}}
	go func() {
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		kind := ""
		for sc.Scan() {
			line := sc.Text()
			if k, ok := strings.CutPrefix(line, "event: "); ok {
				kind = k
			} else if d, ok := strings.CutPrefix(line, "data: "); ok {
				var data map[string]any
				_ = json.Unmarshal([]byte(d), &data)
				l.ch <- [2]any{kind, data}
			}
		}
	}()
	return l
}

func (l *eventLog) waitFor(t *testing.T, match func(kind string, data map[string]any) bool) {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev := <-l.ch:
			kind, data := ev[0].(string), ev[1].(map[string]any)
			l.seen[kind] = true
			if match(kind, data) {
				return
			}
		case <-timeout:
			t.Fatal(fmt.Sprintf("event never arrived; saw %v", l.seen))
		}
	}
}

func (l *eventLog) saw(kind string) bool { return l.seen[kind] }

type urlType = url.URL

func TestBacklog(t *testing.T) {
	e := setup(t)
	dir := t.TempDir()
	for _, name := range []string{"2026-09-27 09-58-01.mkv", "2026-10-04 09-57-37.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if res, body := e.do(t, "POST", "/api/backlogs", map[string]any{"dir": filepath.Join(dir, "nope")}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("missing folder: %d %s", res.StatusCode, body)
	}
	res, body := e.do(t, "POST", "/api/backlogs", map[string]any{"dir": dir})
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"total":2`) {
		t.Fatalf("open: %d %s", res.StatusCode, body)
	}
	if _, body := e.do(t, "GET", "/api/backlogs", nil); !strings.Contains(string(body), `"marked":0`) {
		t.Errorf("list: %s", body)
	}
	_, body = e.do(t, "GET", "/api/jobs", nil)
	var all []jobs.Job
	_ = json.Unmarshal(body, &all)
	if len(all) != 2 || all[0].Backlog != dir {
		t.Fatalf("jobs %+v", all)
	}

	// Marking one writes the folder's backlog.json.
	if res, body := e.do(t, "PATCH", "/api/jobs/"+all[0].ID, map[string]any{"start": 60, "end": 1800, "series": "Fall"}); res.StatusCode != http.StatusOK {
		t.Fatalf("mark: %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, "PATCH", "/api/jobs/"+all[1].ID, map[string]any{"skipped": true}); res.StatusCode != http.StatusOK {
		t.Fatal("skip failed")
	}
	raw, err := os.ReadFile(filepath.Join(dir, backlog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); !strings.Contains(s, `"recording": "2026-09-27 09-58-01.mkv"`) || !strings.Contains(s, `"series": "Fall"`) || !strings.Contains(s, `"skip": true`) {
		t.Errorf("backlog.json:\n%s", raw)
	}
	if _, body := e.do(t, "GET", "/api/backlogs", nil); !strings.Contains(string(body), `"marked":1,"skipped":1`) {
		t.Errorf("after marking: %s", body)
	}

	// Forgetting removes its jobs but keeps the file; reopening restores the marks.
	if res, _ := e.do(t, "POST", "/api/backlogs/forget", map[string]any{"dir": dir}); res.StatusCode != http.StatusOK {
		t.Fatal("forget failed")
	}
	if _, body := e.do(t, "GET", "/api/jobs", nil); string(body) != "null\n" && string(body) != "[]\n" {
		t.Errorf("jobs after forget: %s", body)
	}
	if _, body := e.do(t, "POST", "/api/backlogs", map[string]any{"dir": dir}); !strings.Contains(string(body), `"marked":1,"skipped":1`) {
		t.Errorf("reopened: %s", body)
	}
}

func TestEditSeveralAndImport(t *testing.T) {
	e := setup(t)
	var ids []string
	for range 2 {
		_, body := e.do(t, "POST", "/api/jobs", map[string]any{"recording": e.rec})
		var j jobs.Job
		_ = json.Unmarshal(body, &j)
		ids = append(ids, j.ID)
	}
	if res, body := e.do(t, "POST", "/api/jobs/edit", map[string]any{"ids": ids, "edit": map[string]any{"series": "Advent"}}); res.StatusCode != http.StatusOK {
		t.Fatalf("edit: %d %s", res.StatusCode, body)
	}
	for _, id := range ids {
		_, body := e.do(t, "GET", "/api/jobs/"+id, nil)
		if !strings.Contains(string(body), `"series":"Advent"`) {
			t.Errorf("job %s: %s", id, body)
		}
	}
	// A bad edit changes nothing.
	if res, _ := e.do(t, "POST", "/api/jobs/edit", map[string]any{"ids": ids, "edit": map[string]any{"date": "nope"}}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad edit: %d", res.StatusCode)
	}

	file := filepath.Join(t.TempDir(), "bulk_states.json")
	_ = os.WriteFile(file, []byte(`[{"recording_path": "a.mkv", "raw_begin_offset": 1, "raw_end_offset": 5},
	  {"recording_path": "b.mkv", "raw_begin_offset": null, "raw_end_offset": null}]`), 0o644)
	if res, body := e.do(t, "POST", "/api/import", map[string]any{"path": file}); res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"imported":2`) {
		t.Errorf("import: %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, "POST", "/api/import", map[string]any{"path": file + ".missing"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("missing file: %d", res.StatusCode)
	}
}

func TestSettingsKeepSecrets(t *testing.T) {
	e := setup(t)
	set, _ := e.srv.Store.Settings()
	set.OBS.Password, set.ProPresenter.Password, set.API.Token = "obs-pw", "pp-pw", "tok"
	_ = e.srv.Store.SaveSettings(set)

	_, body := e.do(t, "GET", "/api/settings", nil)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	if strings.Contains(string(body), "obs-pw") || strings.Contains(string(body), "pp-pw") || got["hasOBSPassword"] != true {
		t.Fatalf("settings: %s", body)
	}
	// Saving the form back as-is (blank passwords) keeps them.
	got["obs"].(map[string]any)["host"] = "10.0.0.5"
	res, body := e.do(t, "PUT", "/api/settings", got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", res.StatusCode, body)
	}
	saved, _ := e.srv.Store.Settings()
	if saved.OBS.Host != "10.0.0.5" || saved.OBS.Password != "obs-pw" || saved.ProPresenter.Password != "pp-pw" || saved.API.Token != "tok" {
		t.Errorf("saved %+v", saved)
	}
	// A new password replaces it; clearing removes it.
	got["obs"].(map[string]any)["password"] = "new"
	got["clearPPPassword"] = true
	e.do(t, "PUT", "/api/settings", got)
	saved, _ = e.srv.Store.Settings()
	if saved.OBS.Password != "new" || saved.ProPresenter.Password != "" {
		t.Errorf("passwords %q %q", saved.OBS.Password, saved.ProPresenter.Password)
	}
	// Bad input is refused.
	pp := got["propresenter"].(map[string]any)
	pp["begin_slide"] = map[string]any{"text": "[", "match": "regex"}
	if res, body := e.do(t, "PUT", "/api/settings", got); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad regex: %d %s", res.StatusCode, body)
	}
}

func TestSeriesManagement(t *testing.T) {
	e := setup(t)
	fall := map[string]any{"name": "Fall", "intro": "i.png", "outro": "o.mp4", "transition_duration": 1}
	if res, body := e.do(t, "POST", "/api/series", fall); res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"transition":"fade"`) {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(t, "POST", "/api/series", fall); res.StatusCode != http.StatusConflict {
		t.Errorf("duplicate: %d", res.StatusCode)
	}
	if res, _ := e.do(t, "POST", "/api/series", map[string]any{"name": "X", "intro": "i.png", "outro": "o.mp4"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("no transition length: %d", res.StatusCode)
	}
	_, body := e.do(t, "POST", "/api/jobs", map[string]any{"recording": e.rec, "series": "Fall"})
	var j jobs.Job
	_ = json.Unmarshal(body, &j)

	// Renaming carries over to the job.
	fall["name"] = "Fall 2026"
	if res, body := e.do(t, "PUT", "/api/series/Fall", fall); res.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d %s", res.StatusCode, body)
	}
	if _, body := e.do(t, "GET", "/api/jobs/"+j.ID, nil); !strings.Contains(string(body), `"series":"Fall 2026"`) {
		t.Errorf("job after rename: %s", body)
	}
	if _, err := e.srv.Store.FindSeries("Fall"); err == nil {
		t.Error("old name still exists")
	}
	// Deleting one in use needs force.
	if res, _ := e.do(t, "DELETE", "/api/series/Fall%202026", nil); res.StatusCode != http.StatusConflict {
		t.Errorf("delete in use: %d", res.StatusCode)
	}
	if res, _ := e.do(t, "DELETE", "/api/series/Fall%202026?force=1", nil); res.StatusCode != http.StatusOK {
		t.Errorf("forced delete: %d", res.StatusCode)
	}
}

func TestLiveNeedsTheApp(t *testing.T) {
	e := setup(t)
	if res, _ := e.do(t, "GET", "/api/live", nil); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("live without a manager: %d", res.StatusCode)
	}
}
