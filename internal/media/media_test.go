package media_test

import (
	"context"
	"math"
	"testing"

	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

func TestParseFrameRate(t *testing.T) {
	for in, want := range map[string]float64{
		"30/1": 30, "30000/1001": 30000.0 / 1001, "25": 25, "0/0": 0, "": 0, "abc": 0, "-30/1": 0,
	} {
		if got := media.ParseFrameRate(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("ParseFrameRate(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestProbe(t *testing.T) {
	tools := testmedia.Tools(t)
	path := testmedia.ColorBlocks(t, tools, 4)
	info, err := media.Probe(context.Background(), tools, path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(info.Duration-4) > 0.1 || info.Width != 160 || info.Height != 120 ||
		info.FPS != 30 || info.VideoCodec != "h264" || !info.HasAudio {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestProbeRejectsAudioOnly(t *testing.T) {
	tools := testmedia.Tools(t)
	path := testmedia.Make(t, tools, "tone.m4a", "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "aac")
	if _, err := media.Probe(context.Background(), tools, path); err == nil {
		t.Error("probing an audio-only file succeeded, want an error")
	}
}

func TestProbeImage(t *testing.T) {
	tools := testmedia.Tools(t)
	path := testmedia.Make(t, tools, "still.png", "-f", "lavfi", "-i", "color=c=red:s=64x48", "-frames:v", "1")
	w, h, err := media.ProbeImage(context.Background(), tools, path)
	if err != nil || w != 64 || h != 48 {
		t.Errorf("ProbeImage = %d, %d, %v", w, h, err)
	}
	if !media.IsImage(path) || media.IsImage("clip.mp4") {
		t.Error("IsImage misclassified a file")
	}
}

func TestFindNextKeyframe(t *testing.T) {
	tools := testmedia.Tools(t)
	path := testmedia.ColorBlocks(t, tools, 6) // IDR every 1.5 s
	ctx := context.Background()
	for start, want := range map[float64]float64{0: 0, 0.1: 1.5, 2.2: 3, 3: 3} {
		got, ok, err := media.FindNextKeyframe(ctx, tools, path, start, media.CopyCodecs["h264"])
		if err != nil || !ok || math.Abs(got-want) > 0.01 {
			t.Errorf("FindNextKeyframe(%v) = %v, %v, %v; want %v", start, got, ok, err, want)
		}
	}
	if _, ok, _ := media.FindNextKeyframe(ctx, tools, path, 5, media.CopyCodecs["h264"]); ok {
		t.Error("found a keyframe after the last one")
	}
}
