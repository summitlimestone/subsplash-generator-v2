package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/summitlimestone/subsplash-generator-v2/frontend"
	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/render"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
)

// selfTest checks an installed copy works without opening a window: the
// bundled ffmpeg runs and renders, the database opens and the UI is
// embedded. It reports each check to out and returns whether all passed.
func selfTest(out io.Writer) bool {
	fmt.Fprintf(out, "%s %s self-test\n", appName, version)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "sg-selftest-")
	if err != nil {
		fmt.Fprintln(out, "FAIL temp folder:", err)
		return false
	}
	defer os.RemoveAll(dir)

	var tools ffmpeg.Tools
	file := func(name string) string { return filepath.Join(dir, name) }
	checks := []struct {
		name string
		run  func() error
	}{
		{"bundled ffmpeg", func() (err error) {
			tools, err = ffmpeg.Locate()
			if err != nil {
				return err
			}
			v, err := tools.Output(ctx, []string{"-version"})
			if err == nil {
				fmt.Fprintf(out, "     %.60s\n", v)
			}
			return err
		}},
		{"make a test recording", func() error {
			return tools.Run(ctx, []string{"-y",
				"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=6",
				"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
				"-c:v", "libx264", "-g", "45", "-c:a", "aac", "-shortest", file("recording.mp4")}, nil)
		}},
		{"make an intro image", func() error {
			return tools.Run(ctx, []string{"-y", "-f", "lavfi", "-i", "color=c=navy:size=640x360", "-frames:v", "1", file("intro.png")}, nil)
		}},
		{"trim", func() error {
			r := render.Runner{Tools: tools}
			err := r.Trim(ctx, file("recording.mp4"), file("trimmed.mp4"), render.Trim{
				Start: 1, End: 4, FastCopy: true, Normalize: true, TargetLUFS: -16,
				Encode: render.Encode{Encoder: render.Software, CRF: 23},
			})
			if err != nil {
				return err
			}
			return expectDuration(ctx, tools, file("trimmed.mp4"), 3)
		}},
		{"stitch", func() error {
			r := render.Runner{Tools: tools}
			err := r.Stitch(ctx, file("trimmed.mp4"), file("final.mp4"), render.Stitch{
				Intro:      render.Clip{Path: file("intro.png"), ImageDuration: 1},
				Outro:      render.Clip{Path: file("recording.mp4")},
				Transition: "fade", TransitionDuration: 0.5,
				Encode:    render.Encode{Encoder: render.Software, CRF: 23},
				Subsplash: true,
			})
			if err != nil {
				return err
			}
			// 1 + 3 + 6, less two half-second crossfades.
			return expectDuration(ctx, tools, file("final.mp4"), 9)
		}},
		{"database", func() error {
			st, err := store.Open(file("test.db"))
			if err != nil {
				return err
			}
			defer st.Close()
			_, err = st.Settings()
			return err
		}},
		{"user interface", func() error {
			_, err := fs.Stat(frontend.Dist, "dist/index.html")
			return err
		}},
	}
	for _, c := range checks {
		if err := c.run(); err != nil {
			fmt.Fprintf(out, "FAIL %s: %v\n", c.name, err)
			return false
		}
		fmt.Fprintf(out, "ok   %s\n", c.name)
	}
	return true
}

func expectDuration(ctx context.Context, t ffmpeg.Tools, path string, want float64) error {
	info, err := media.Probe(ctx, t, path)
	if err != nil {
		return err
	}
	if math.Abs(info.Duration-want) > 0.2 {
		return fmt.Errorf("%s is %.2f s long, want %.2f s", filepath.Base(path), info.Duration, want)
	}
	return nil
}
