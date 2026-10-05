// Package testmedia generates small media files for tests with real
// ffmpeg and inspects the results.
package testmedia

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
)

// Tools returns the ffmpeg tools for tests: SG_FFMPEG_DIR if set, else
// the copy on PATH. Only tests may look on PATH; the app never does.
func Tools(tb testing.TB) ffmpeg.Tools {
	tb.Helper()
	if os.Getenv(ffmpeg.DevDirEnv) == "" {
		p, err := exec.LookPath("ffmpeg")
		if err != nil {
			tb.Skip("ffmpeg not found; set " + ffmpeg.DevDirEnv)
		}
		tb.Setenv(ffmpeg.DevDirEnv, filepath.Dir(p))
	}
	t, err := ffmpeg.Locate()
	if err != nil {
		tb.Fatal(err)
	}
	return t
}

// Make runs ffmpeg with args, writing to name in a fresh temp dir, and
// returns the output path.
func Make(tb testing.TB, t ffmpeg.Tools, name string, args ...string) string {
	tb.Helper()
	out := filepath.Join(tb.TempDir(), name)
	full := append([]string{"-y"}, args...)
	if err := t.Run(context.Background(), append(full, out), nil); err != nil {
		tb.Fatalf("making %s: %v", name, err)
	}
	return out
}

// Colors are the solid colors of the blocks in ColorBlocks, in order.
var Colors = [][3]uint8{
	{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 0}, {255, 0, 255}, {0, 255, 255},
}

// ColorBlocks makes an H.264/AAC MP4 of n one-second solid-color blocks
// cycling through Colors, at 30 fps with an IDR frame every 1.5 s (so
// block edges and keyframes don't line up), plus a 440 Hz tone.
func ColorBlocks(tb testing.TB, t ffmpeg.Tools, n int) string {
	tb.Helper()
	return ColorBlocksAs(tb, t, n, ".mp4")
}

// ColorBlocksAs is ColorBlocks in the container named by ext, e.g. ".mkv".
func ColorBlocksAs(tb testing.TB, t ffmpeg.Tools, n int, ext string) string {
	tb.Helper()
	filter := ""
	inputs := ""
	for i := range n {
		c := Colors[i%len(Colors)]
		filter += fmt.Sprintf("color=c=0x%02X%02X%02X:s=160x120:r=30:d=1[c%d];", c[0], c[1], c[2], i)
		inputs += fmt.Sprintf("[c%d]", i)
	}
	filter += fmt.Sprintf("%sconcat=n=%d:v=1:a=0[v]", inputs, n)
	return Make(tb, t, "blocks"+ext,
		"-filter_complex", filter,
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%d", n),
		"-map", "[v]", "-map", "0:a",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-x264-params", "keyint=45:min-keyint=45:scenecut=0",
		"-c:a", "aac", "-shortest",
	)
}

// FrameColor returns the average color of the frame at time at in path.
func FrameColor(tb testing.TB, t ffmpeg.Tools, path string, at float64) [3]uint8 {
	tb.Helper()
	out := filepath.Join(tb.TempDir(), "frame.rgb")
	if err := t.Run(context.Background(), []string{
		"-y", "-ss", fmt.Sprintf("%.3f", at), "-i", path, "-frames:v", "1",
		"-vf", "scale=1:1:flags=area", "-f", "rawvideo", "-pix_fmt", "rgb24", out,
	}, nil); err != nil {
		tb.Fatalf("reading frame at %.3f: %v", at, err)
	}
	b, err := os.ReadFile(out)
	if err != nil || len(b) < 3 {
		tb.Fatalf("reading frame at %.3f: got %d bytes, %v", at, len(b), err)
	}
	return [3]uint8{b[0], b[1], b[2]}
}

// Near reports whether two colors are within a codec's rounding of each other.
func Near(a, b [3]uint8) bool {
	for i := range 3 {
		d := int(a[i]) - int(b[i])
		if d < -40 || d > 40 {
			return false
		}
	}
	return true
}

// StreamDurations returns each stream's own duration in seconds by type.
func StreamDurations(tb testing.TB, t ffmpeg.Tools, path string) map[string]float64 {
	tb.Helper()
	return streamField(tb, t, path, "duration")
}

// StreamStarts returns each stream's start time in seconds by type.
func StreamStarts(tb testing.TB, t ffmpeg.Tools, path string) map[string]float64 {
	tb.Helper()
	return streamField(tb, t, path, "start_time")
}

func streamField(tb testing.TB, t ffmpeg.Tools, path, field string) map[string]float64 {
	tb.Helper()
	out, err := t.Probe(context.Background(), []string{"-show_entries", "stream=codec_type," + field, "-of", "json", path})
	if err != nil {
		tb.Fatal(err)
	}
	var p struct {
		Streams []map[string]any `json:"streams"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		tb.Fatal(err)
	}
	d := map[string]float64{}
	for _, s := range p.Streams {
		v, _ := strconv.ParseFloat(fmt.Sprint(s[field]), 64)
		d[fmt.Sprint(s["codec_type"])] = v
	}
	return d
}
