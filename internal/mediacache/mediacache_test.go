package mediacache

import (
	"context"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/internal/testmedia"
)

func TestPeaksFollowLoudness(t *testing.T) {
	tools := testmedia.Tools(t)
	// 2 s of silence, 2 s of a loud tone, 2 s of a quiet one. (sine is
	// 1/8 amplitude, so these are 0.5 and 0.01.)
	clip := testmedia.Make(t, tools, "levels.mkv",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:r=10:d=6",
		"-f", "lavfi", "-i", "sine=f=440:sample_rate=48000:d=6,volume='if(lt(t,2),0,if(lt(t,4),4,0.08))':eval=frame",
		"-c:v", "libx264", "-c:a", "aac", "-shortest")
	c := New(t.TempDir(), tools, nil)
	var updates atomic.Int32
	c.OnPeaks = func(string) { updates.Add(1) }
	var data []byte
	deadline := time.Now().Add(30 * time.Second)
	for {
		d, complete, err := c.Peaks(clip)
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			data = d
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waveform never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(data); n < 6*PeaksPerSecond-2 || n > 6*PeaksPerSecond+2 {
		t.Fatalf("%d levels for 6 s", n)
	}
	at := func(sec float64) byte { return data[int(sec*PeaksPerSecond)] }
	silent, loud, quiet := at(1), at(3), at(5)
	if !(silent < 20 && loud > 200 && quiet > 60 && quiet < loud-60) {
		t.Errorf("levels silent=%d loud=%d quiet=%d", silent, loud, quiet)
	}
	if updates.Load() == 0 {
		t.Error("no update notifications")
	}
	// A second cache over the same folder reads the finished file.
	if d, complete, err := New(c.dir, tools, nil).Peaks(clip); err != nil || !complete || len(d) != len(data) {
		t.Errorf("cached waveform: %d bytes, complete=%v, %v", len(d), complete, err)
	}
}

func TestPeaksOfAFileWithoutAudio(t *testing.T) {
	tools := testmedia.Tools(t)
	clip := testmedia.Make(t, tools, "silent.mp4", "-f", "lavfi", "-i", "color=c=black:s=64x64:r=10:d=1", "-c:v", "libx264")
	c := New(t.TempDir(), tools, nil)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, complete, err := c.Peaks(clip)
		if complete {
			if err == nil {
				t.Error("expected an error for a file without audio")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("never finished")
}

func TestThumbnail(t *testing.T) {
	tools := testmedia.Tools(t)
	clip := testmedia.ColorBlocks(t, tools, 4)
	c := New(t.TempDir(), tools, nil)
	file, err := c.Thumbnail(context.Background(), clip, 2.5, 72)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil || img.Bounds().Dy() != 72 {
		t.Fatalf("thumbnail %v, %v", img.Bounds(), err)
	}
	// Cached: the same file again, without running ffmpeg.
	again, err := c.Thumbnail(context.Background(), clip, 2.5, 72)
	if err != nil || again != file {
		t.Errorf("second call gave %q, %v", again, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(file), "*.tmp.jpg")); len(leftovers) > 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}
