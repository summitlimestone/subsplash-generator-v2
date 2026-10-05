package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// concatTimescale is forced onto every segment that gets joined. The
// concat demuxer doesn't rescale between segments: a re-encoded segment
// (e.g. 15360 ticks/s) joined to a stream copy (e.g. 16000) plays the
// copy about 4% slow, drifting video out of sync with audio. 90 kHz
// divides evenly into every common frame rate.
const concatTimescale = 90000

var timescaleArgs = []string{"-video_track_timescale", strconv.Itoa(concatTimescale)}

// joinVerifyWindow is how much video is decoded around each join.
const joinVerifyWindow = 2.0

// concat stream-copies parts into dst, then checks the video decodes
// cleanly around each join offset (seconds into dst). Two separately
// encoded segments can carry codec parameters MP4 can't hold side by
// side, which only shows up as decode errors from the join onward.
func (r *Runner) concat(ctx context.Context, parts []string, dst string, joins []float64, duration float64) error {
	var list strings.Builder
	for _, p := range parts {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(abs, "'", `'\''`))
	}
	listPath := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".concat.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return err
	}
	defer os.Remove(listPath)

	if err := r.run(ctx, "joining", duration, []string{
		"-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", dst,
	}); err != nil {
		return err
	}
	for _, at := range joins {
		start := max(0, at-joinVerifyWindow/2)
		stderr, err := r.Tools.Output(ctx, []string{
			"-v", "error", "-ss", fmt.Sprintf("%.3f", start), "-i", dst,
			"-t", fmt.Sprintf("%.3f", joinVerifyWindow), "-map", "0:v:0", "-f", "null", "-",
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || strings.TrimSpace(stderr) != "" {
			return fmt.Errorf("joined video doesn't decode cleanly at %.3fs", at)
		}
	}
	return nil
}
