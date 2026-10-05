package render

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

// logRecorder collects warning messages so tests can check fallbacks.
type logRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func (l *logRecorder) contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range l.msgs {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

func newRunner(t *testing.T) (*Runner, *logRecorder) {
	rec := &logRecorder{}
	r := &Runner{Tools: testmedia.Tools(t), Log: newTestLogger(rec)}
	return r, rec
}

func probe(t *testing.T, r *Runner, path string) media.Info {
	t.Helper()
	info, err := media.Probe(context.Background(), r.Tools, path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// checkTrim checks a trim of testmedia.ColorBlocks from start to end:
// its length, its first and last frames, and that audio and video stay
// the same length (the concat timescale bug stretched video by ~4%).
func checkTrim(t *testing.T, r *Runner, out string, start, end float64) {
	t.Helper()
	info := probe(t, r, out)
	if want := end - start; math.Abs(info.Duration-want) > 0.1 {
		t.Errorf("duration %.3f, want %.3f", info.Duration, want)
	}
	first := testmedia.Colors[int(start)%len(testmedia.Colors)]
	if got := testmedia.FrameColor(t, r.Tools, out, 0); !testmedia.Near(got, first) {
		t.Errorf("first frame %v, want %v", got, first)
	}
	last := testmedia.Colors[int(end-0.01)%len(testmedia.Colors)]
	if got := testmedia.FrameColor(t, r.Tools, out, info.Duration-0.1); !testmedia.Near(got, last) {
		t.Errorf("last frame %v, want %v", got, last)
	}
	d := testmedia.StreamDurations(t, r.Tools, out)
	if math.Abs(d["video"]-d["audio"]) > 0.1 {
		t.Errorf("video %.3fs and audio %.3fs drifted apart", d["video"], d["audio"])
	}
}

func TestTrimFastCopy(t *testing.T) {
	// MKV is what OBS recorded in v1, and the container whose stream copy
	// exposed the concat timescale bug.
	for _, ext := range []string{".mp4", ".mkv"} {
		t.Run(ext, func(t *testing.T) {
			r, rec := newRunner(t)
			src := testmedia.ColorBlocksAs(t, r.Tools, 12, ext)
			out := filepath.Join(t.TempDir(), "out", "trim.mp4")
			if err := r.Trim(context.Background(), src, out, Trim{Start: 2.2, End: 10.6, FastCopy: true, Encode: Encode{CRF: 23}}); err != nil {
				t.Fatal(err)
			}
			if rec.contains("fast copy not used") {
				t.Error("fell back to a full re-encode")
			}
			checkTrim(t, r, out, 2.2, 10.6)
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(out), ".*"))
			if len(leftovers) > 0 {
				t.Errorf("temporary files left behind: %v", leftovers)
			}
		})
	}
}

func TestTrimFastCopyOnAKeyframe(t *testing.T) {
	r, rec := newRunner(t)
	src := testmedia.ColorBlocks(t, r.Tools, 8)
	out := filepath.Join(t.TempDir(), "trim.mp4")
	if err := r.Trim(context.Background(), src, out, Trim{Start: 3, End: 7.5, FastCopy: true}); err != nil {
		t.Fatal(err)
	}
	if rec.contains("fast copy not used") {
		t.Error("fell back to a full re-encode")
	}
	checkTrim(t, r, out, 3, 7.5)
}

func TestTrimFullReencode(t *testing.T) {
	r, _ := newRunner(t)
	src := testmedia.ColorBlocks(t, r.Tools, 8)
	out := filepath.Join(t.TempDir(), "trim.mp4")
	if err := r.Trim(context.Background(), src, out, Trim{Start: 1.4, End: 6.3, Encode: Encode{Encoder: Software, CRF: 30}}); err != nil {
		t.Fatal(err)
	}
	checkTrim(t, r, out, 1.4, 6.3)
}

func TestTrimShorterThanAGOPReencodes(t *testing.T) {
	r, rec := newRunner(t)
	src := testmedia.ColorBlocks(t, r.Tools, 6)
	out := filepath.Join(t.TempDir(), "trim.mp4")
	if err := r.Trim(context.Background(), src, out, Trim{Start: 1.6, End: 2.8, FastCopy: true, Encode: Encode{CRF: 30}}); err != nil {
		t.Fatal(err)
	}
	if !rec.contains("fast copy not used") {
		t.Error("expected a fallback to a full re-encode")
	}
	checkTrim(t, r, out, 1.6, 2.8)
}

func TestTrimRejectsBadRange(t *testing.T) {
	r, _ := newRunner(t)
	for _, o := range []Trim{{Start: 5, End: 5}, {Start: 6, End: 2}, {Start: -1, End: 2}} {
		if err := r.Trim(context.Background(), "unused.mp4", filepath.Join(t.TempDir(), "x.mp4"), o); err == nil {
			t.Errorf("Trim(%+v) succeeded, want an error", o)
		}
	}
}

// integratedLoudness measures a file's integrated loudness in LUFS.
func integratedLoudness(t *testing.T, r *Runner, path string) float64 {
	t.Helper()
	info := probe(t, r, path)
	m, ok, err := r.measureLoudness(context.Background(), path, 0, info.Duration, -16)
	if err != nil || !ok {
		t.Fatalf("measuring %s: ok=%v err=%v", path, ok, err)
	}
	v, err := strconv.ParseFloat(m.I, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTrimNormalizesLoudness(t *testing.T) {
	r, _ := newRunner(t)
	src := testmedia.Make(t, r.Tools, "quiet.mp4",
		"-f", "lavfi", "-i", "testsrc2=s=160x120:r=30:d=10",
		"-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000:duration=10,volume=0.03",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-x264-params", "keyint=45:min-keyint=45:scenecut=0",
		"-c:a", "aac", "-shortest")
	if before := integratedLoudness(t, r, src); before > -28 {
		t.Fatalf("source isn't quiet enough to test with: %.1f LUFS", before)
	}
	for _, fast := range []bool{true, false} {
		out := filepath.Join(t.TempDir(), "norm.mp4")
		if err := r.Trim(context.Background(), src, out, Trim{Start: 1.2, End: 8.8, FastCopy: fast, Normalize: true, TargetLUFS: -16}); err != nil {
			t.Fatal(err)
		}
		if got := integratedLoudness(t, r, out); math.Abs(got+16) > 1.5 {
			t.Errorf("fast copy %v: loudness %.1f LUFS, want -16", fast, got)
		}
	}
}

func TestTrimSilentAudioSkipsNormalizing(t *testing.T) {
	r, rec := newRunner(t)
	src := testmedia.Make(t, r.Tools, "silent.mp4",
		"-f", "lavfi", "-i", "testsrc2=s=160x120:r=30:d=4",
		"-f", "lavfi", "-i", "anullsrc=sample_rate=48000",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	out := filepath.Join(t.TempDir(), "trim.mp4")
	if err := r.Trim(context.Background(), src, out, Trim{Start: 0.5, End: 3.5, Normalize: true, TargetLUFS: -16}); err != nil {
		t.Fatal(err)
	}
	if !rec.contains("not normalizing") {
		t.Error("expected normalizing to be skipped")
	}
}

func TestEncoderFallsBackToSoftware(t *testing.T) {
	r, rec := newRunner(t)
	Encoders["broken"] = Encoder{
		Codec: "no_such_encoder", Presets: []string{"x"}, DefaultPreset: "x",
		qualityArgs: func(int, string) []string { return nil },
		bitrateArgs: func(int, string) []string { return nil },
	}
	t.Cleanup(func() { delete(Encoders, "broken") })
	src := testmedia.ColorBlocks(t, r.Tools, 4)
	out := filepath.Join(t.TempDir(), "trim.mp4")
	if err := r.Trim(context.Background(), src, out, Trim{Start: 1, End: 3, Encode: Encode{Encoder: "broken", CRF: 30}}); err != nil {
		t.Fatal(err)
	}
	if !rec.contains("falling back to software") {
		t.Error("expected a software fallback")
	}
	checkTrim(t, r, out, 1, 3)
}

func TestTrimCancel(t *testing.T) {
	r, _ := newRunner(t)
	src := testmedia.Make(t, r.Tools, "long.mp4",
		"-f", "lavfi", "-i", "testsrc2=s=640x360:r=30:d=60", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
	ctx, cancel := context.WithCancel(context.Background())
	r.OnProgress = func(Step) { cancel() }
	out := filepath.Join(t.TempDir(), "trim.mp4")
	start := time.Now()
	err := r.Trim(ctx, src, out, Trim{Start: 0, End: 59, Encode: Encode{Encoder: Software, Preset: "veryslow", CRF: 18}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("cancelling took too long")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("partial output left behind")
	}
}

func TestProgressReachesTheEnd(t *testing.T) {
	r, _ := newRunner(t)
	var last Step
	r.OnProgress = func(s Step) { last = s }
	src := testmedia.ColorBlocks(t, r.Tools, 4)
	if err := r.Trim(context.Background(), src, filepath.Join(t.TempDir(), "t.mp4"), Trim{Start: 0.5, End: 3.5, Encode: Encode{CRF: 30}}); err != nil {
		t.Fatal(err)
	}
	if last.Label != "trimming" || last.Fraction != 1 {
		t.Errorf("last progress %+v, want trimming at 1", last)
	}
}
