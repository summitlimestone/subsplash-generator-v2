package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/appdir"
	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

func writeJSON(t *testing.T, path string, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBulkRendersAV1BulkFile imports v1's config and series, then renders
// a v1 bulk states file unattended: the milestone 2 acceptance test.
func TestBulkRendersAV1BulkFile(t *testing.T) {
	tools := testmedia.Tools(t)
	rec := testmedia.ColorBlocks(t, tools, 8)
	intro := testmedia.Make(t, tools, "intro.png", "-f", "lavfi", "-i", "color=c=white:s=160x120", "-frames:v", "1")
	dir, data := t.TempDir(), t.TempDir()
	out := filepath.Join(dir, "videos")

	config := writeJSON(t, filepath.Join(dir, "config.json"), map[string]any{
		"trim":   map[string]any{"output": filepath.Join(out, "%Y-%m-%d", "body.mp4"), "crf": 30, "encoder": "software", "encoder_preset": "ultrafast", "fast_copy": true},
		"stitch": map[string]any{"output": filepath.Join(out, "final", "%Y-%m-%d.mp4"), "subsplash_preset": false, "encoder": "software"},
	})
	series := writeJSON(t, filepath.Join(dir, "series.json"), []map[string]any{
		{"name": "Fall", "intro": intro, "intro_duration": 1, "outro": intro, "outro_duration": 1, "transition": "fade", "transition_duration": 0.5},
	})
	// Two sermons from one recording, as the v1 bulk tab wrote them.
	entry := func(begin, end string) map[string]any {
		return map[string]any{
			"recording_path": rec, "raw_begin_offset": begin, "raw_end_offset": end, "trimmed_path": nil,
			"trim":   map[string]any{"output": "body_trimmed.mp4"}, // shared by both: v2 names outputs itself
			"stitch": map[string]any{"series": "Fall", "output": "%Y-%m-%d_subsplash.mp4"},
		}
	}
	bulk := writeJSON(t, filepath.Join(dir, "bulk_states.json"), []any{entry("00:00:01.200", "00:00:05.000"), entry("00:00:02.000", "00:00:07.500")})
	// The shared literal trim output above would make the two jobs write
	// one file, which validation must refuse...
	err := run([]string{"bulk", "-data", data, bulk})
	if err == nil || !strings.Contains(err.Error(), "nothing was queued") {
		t.Fatalf("shared output: got %v", err)
	}
	// ...so drop it, and v2 names each from its date.
	var entries []map[string]any
	raw, _ := os.ReadFile(bulk)
	_ = json.Unmarshal(raw, &entries)
	for _, e := range entries {
		delete(e["trim"].(map[string]any), "output")
	}
	writeJSON(t, bulk, entries)

	if err := run([]string{"import", "-data", data, "-config", config, "-series", series}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"bulk", "-data", data, bulk}); err != nil {
		t.Fatal(err)
	}
	// testmedia names its clip blocks.mp4, so there's no date: outputs are
	// named by job ID. Expect two trims and two finals of the right length.
	trims, _ := filepath.Glob(filepath.Join(out, "*_trimmed.mp4"))
	finals, _ := filepath.Glob(filepath.Join(out, "final", "*.mp4"))
	if len(trims) != 2 || len(finals) != 2 {
		t.Fatalf("trims %v finals %v", trims, finals)
	}
	for _, f := range finals {
		info, err := media.Probe(context.Background(), tools, f)
		if err != nil {
			t.Fatal(err)
		}
		if d := info.Duration; !(d > 4.4 && d < 4.9) && !(d > 6.4 && d < 6.9) {
			t.Errorf("%s is %.2fs", filepath.Base(f), d)
		}
	}
	if err := run([]string{"jobs", "-data", data}); err != nil {
		t.Fatal(err)
	}
	// The rejected first run left nothing behind.
	st, err := dataStore(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if all, _ := st.Jobs(); len(all) != 2 {
		t.Errorf("%d jobs in the store, want 2", len(all))
	}
}

func dataStore(dir string) (*store.Store, error) { return store.Open(appdir.Database(dir)) }
