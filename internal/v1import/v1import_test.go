package v1import

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The OBS machine's real v1 settings, apart from passwords.
const realConfig = `{
  "api": {"enabled": true, "host": "0.0.0.0", "port": 8765, "password": "secret"},
  "propresenter": {"host": "192.168.1.50", "port": 1025, "password": "pp", "reconnect_interval_seconds": 4,
    "begin_slide": {"uid": "90F4618D"}, "end_slide": {"text": "Amen", "match_mode": "substring"}},
  "obs": {"host": "localhost", "port": 4455, "password": "obs"},
  "trim": {"output": "D:\\video\\%Y-%m-%d\\body_trimmed_%H-%M-%S.mp4", "pad_start_seconds": 0.6, "pad_end_seconds": 0,
    "crf": 18, "fast_copy": true, "normalize_audio": true, "normalize_target_lufs": -16.0, "encoder": "nvenc", "encoder_preset": "p4"},
  "stitch": {"auto": true, "output": "G:\\Shared drives\\Media\\edited videos\\%Y-%m-%d_subsplash.mp4",
    "subsplash_preset": true, "fast_copy": false, "encoder": "nvenc"}
}`

func TestConfig(t *testing.T) {
	path := write(t, t.TempDir(), "config.json", "\ufeff"+realConfig)
	s, err := Config(path, jobs.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if s.TrimmedDir != filepath.FromSlash("D:/video") || s.FinalDir != filepath.FromSlash("G:/Shared drives/Media/edited videos") {
		t.Errorf("dirs %q %q", s.TrimmedDir, s.FinalDir)
	}
	if s.PadStart != 0.6 || s.Render.TrimCRF != 18 || s.Render.Encoder != "nvenc" || s.Render.Preset != "p4" ||
		!s.Render.Subsplash || !s.Render.FastCopy || s.Render.TargetLUFS != -16 {
		t.Errorf("render settings %+v pad %v", s.Render, s.PadStart)
	}
	if s.OBS.Password != "obs" || s.ProPresenter.Host != "192.168.1.50" || s.ProPresenter.BeginSlide.UID != "90F4618D" {
		t.Errorf("connections %+v %+v", s.OBS, s.ProPresenter)
	}
	if es := s.ProPresenter.EndSlide; es.Match != "regex" || es.Text != "Amen" {
		t.Errorf("substring slide became %+v", es)
	}
	if !s.API.Enabled || s.API.Host != "0.0.0.0" || s.API.Token != "" {
		t.Errorf("api %+v", s.API)
	}
}

func TestOutputDir(t *testing.T) {
	for in, want := range map[string]string{
		`D:\video\%Y-%m-%d\body.mp4`: "D:/video",
		`D:\video\final.mp4`:         "D:/video",
		`\\server\share\%Y\x.mp4`:    "//server/share",
		"body_trimmed.mp4":           "",
		"out/%Y/x.mp4":               "",
		`%Y\x.mp4`:                   "",
	} {
		if got := outputDir(in); got != filepath.FromSlash(want) {
			t.Errorf("outputDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeries(t *testing.T) {
	path := write(t, t.TempDir(), "series.json", `[
	  {"name": "Fall 2026", "intro": "C:/i.png", "intro_duration": 4, "outro": "C:/o.mp4", "outro_duration": 5,
	   "transition": "fadeblack", "transition_duration": 1.5, "hidden": false},
	  {"name": "Old", "intro": "a", "outro": "b"},
	  {"intro": "nameless"}]`)
	list, err := Series(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Transition != "fadeblack" || list[0].IntroDuration != 4 ||
		list[1].Transition != "fade" || list[1].TransitionDuration != 1 {
		t.Errorf("series %+v", list)
	}
}

func TestStatesRenderStateFile(t *testing.T) {
	dir := t.TempDir()
	// A render-state file as v1's watch wrote it on the OBS machine.
	path := write(t, dir, "render_state.json", `{
	  "recording_path": "D:/video/2026-10-04 09-57-37.mkv",
	  "raw_begin_offset": "00:01:22.475", "raw_end_offset": "00:34:06.419", "trimmed_path": null,
	  "trim": {"output": "D:\\video\\%Y-%m-%d\\body_trimmed_%H-%M-%S.mp4", "pad_start_seconds": 0.6, "pad_end_seconds": 0.0,
	    "crf": 18, "fast_copy": true, "normalize_audio": true, "normalize_target_lufs": -16.0, "encoder": "nvenc", "encoder_preset": "p4"},
	  "stitch": {"auto": true, "output": "G:\\x\\%Y-%m-%d_subsplash.mp4", "subsplash_preset": true, "fast_copy": false, "encoder": "nvenc"}
	}`)
	js, err := States(path, jobs.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 1 {
		t.Fatalf("%d jobs", len(js))
	}
	j := js[0]
	if j.Recording != "D:/video/2026-10-04 09-57-37.mkv" || *j.Start != 83.075 || *j.End != 2046.419 {
		t.Errorf("recording/marks %q %v %v", j.Recording, *j.Start, *j.End)
	}
	if j.Date != "2026-10-04" || j.TrimOutput != "" || j.FinalOutput != "" || j.Status != jobs.StatusReady {
		t.Errorf("job %+v", j)
	}
}

func TestStatesBulkFileFromSermonMarker(t *testing.T) {
	dir := t.TempDir()
	trimmed := write(t, dir, "output/2024-01-14_trimmed.mp4", "")
	path := write(t, dir, "bulk_states.json", `[
	  {"recording_path": "input/2024/0107.mkv", "raw_begin_offset": "00:31:04.250", "raw_end_offset": "01:12:40.000",
	   "trimmed_path": null, "trim": {"output": "output/2024-01-07_trimmed.mp4"},
	   "stitch": {"series": "Fall", "output": "output/2024-01-07.mp4"}},
	  {"recording_path": "input\\2024\\0114.mkv", "raw_begin_offset": 100, "raw_end_offset": 200.5,
	   "trimmed_path": "output/2024-01-14_trimmed.mp4", "trim": {"output": "output/2024-01-14_trimmed.mp4"},
	   "stitch": {"series": "Fall", "output": "output/2024-01-14.mp4"}},
	  {"recording_path": "input/unmarked.mkv", "raw_begin_offset": null, "raw_end_offset": null}
	]`)
	s := jobs.DefaultSettings()
	s.PadStart = 5 // live-mark padding; must not apply to hand-placed marks
	js, err := States(path, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 3 {
		t.Fatalf("%d jobs", len(js))
	}
	a, b, c := js[0], js[1], js[2]
	if a.Recording != filepath.Join(dir, "input", "2024", "0107.mkv") || a.Date != "2024-01-07" || a.Series != "Fall" ||
		a.FinalOutput != filepath.Join(dir, "output", "2024-01-07.mp4") || *a.Start != 1864.25 {
		t.Errorf("first job %+v start %v", a, *a.Start)
	}
	if b.Recording != filepath.Join(dir, "input", "2024", "0114.mkv") || *b.Start != 100 || *b.End != 200.5 ||
		b.Trimmed != trimmed || b.Status != jobs.StatusTrimmed {
		t.Errorf("second job %+v", b)
	}
	if c.Status != jobs.StatusDraft || c.Start != nil {
		t.Errorf("unmarked job %+v", c)
	}
}

func TestStatesRejectsBadTimestamps(t *testing.T) {
	path := write(t, t.TempDir(), "s.json", `[{"recording_path": "a.mkv", "raw_begin_offset": "1:2", "raw_end_offset": 5}]`)
	if _, err := States(path, jobs.DefaultSettings()); err == nil {
		t.Error("a malformed timestamp was accepted")
	}
}
