package render

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

func stitchInputs(t *testing.T, r *Runner) (intro, main, outro string) {
	intro = testmedia.Make(t, r.Tools, "intro.png", "-f", "lavfi", "-i", "color=c=white:s=320x240", "-frames:v", "1")
	main = testmedia.ColorBlocks(t, r.Tools, 4) // 160x120 with audio
	// A video outro with no audio track, so the silent-track path runs.
	outro = testmedia.Make(t, r.Tools, "outro.mp4",
		"-f", "lavfi", "-i", "color=c=black:s=320x180:r=25:d=2", "-c:v", "libx264", "-pix_fmt", "yuv420p")
	return
}

func TestStitch(t *testing.T) {
	r, _ := newRunner(t)
	intro, main, outro := stitchInputs(t, r)
	out := filepath.Join(t.TempDir(), "final.mp4")
	err := r.Stitch(context.Background(), main, out, Stitch{
		Intro: Clip{Path: intro, ImageDuration: 2}, Outro: Clip{Path: outro},
		TransitionDuration: 0.5, Encode: Encode{Encoder: Software, CRF: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := probe(t, r, out)
	if want := 2 + 4 + 2 - 2*0.5; math.Abs(info.Duration-want) > 0.15 {
		t.Errorf("duration %.3f, want %.3f", info.Duration, want)
	}
	if info.Width != 160 || info.Height != 120 || info.FPS != 30 || !info.HasAudio {
		t.Errorf("unexpected output: %+v", info)
	}
	// Mid-intro is white, mid-body shows the third block (blue), the end is black.
	for _, c := range []struct {
		at   float64
		want [3]uint8
	}{{1, [3]uint8{255, 255, 255}}, {1.5 + 2.5, testmedia.Colors[2]}, {info.Duration - 0.2, [3]uint8{0, 0, 0}}} {
		if got := testmedia.FrameColor(t, r.Tools, out, c.at); !testmedia.Near(got, c.want) {
			t.Errorf("frame at %.2fs is %v, want %v", c.at, got, c.want)
		}
	}
}

func TestStitchSubsplashPreset(t *testing.T) {
	r, _ := newRunner(t)
	intro, main, outro := stitchInputs(t, r)
	out := filepath.Join(t.TempDir(), "final.mp4")
	err := r.Stitch(context.Background(), main, out, Stitch{
		Intro: Clip{Path: intro, ImageDuration: 1}, Outro: Clip{Path: outro},
		TransitionDuration: 0.5, Subsplash: true, Encode: Encode{Encoder: Software},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := r.Tools.Probe(context.Background(), []string{
		"-select_streams", "v:0", "-show_entries", "stream=width,height,profile,level,r_frame_rate", "-of", "json", out,
	})
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Streams []struct {
			Width, Height, Level int
			Profile              string
			RFrameRate           string `json:"r_frame_rate"`
		}
	}
	if err := json.Unmarshal(raw, &p); err != nil || len(p.Streams) != 1 {
		t.Fatalf("probe: %v %s", err, raw)
	}
	s := p.Streams[0]
	if s.Width != 1920 || s.Height != 1080 || s.Profile != "High" || s.Level != 40 || s.RFrameRate != "30/1" {
		t.Errorf("not Subsplash 1080p: %+v", s)
	}
}

func TestStitchRejectsBadInputs(t *testing.T) {
	r, _ := newRunner(t)
	intro, main, outro := stitchInputs(t, r)
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "final.mp4")
	cases := map[string]Stitch{
		"too short for a 0.5s transition":      {Intro: Clip{Path: intro, ImageDuration: 0.4}, Outro: Clip{Path: outro}, TransitionDuration: 0.5},
		"intro and outro are both required":    {Intro: Clip{Path: intro}, TransitionDuration: 0.5},
		"transition duration must be positive": {Intro: Clip{Path: intro}, Outro: Clip{Path: outro}},
	}
	for want, o := range cases {
		if err := r.Stitch(ctx, main, out, o); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want an error containing %q", err, want)
		}
	}
	if err := r.Stitch(ctx, intro, out, Stitch{Intro: Clip{Path: intro}, Outro: Clip{Path: outro}, TransitionDuration: 0.5}); err == nil {
		t.Error("stitching a still image as the main clip succeeded")
	}
}
