package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/timestamp"
)

// Step is one render step of a job.
type Step string

const (
	StepTrim   Step = "trim"
	StepStitch Step = "stitch"
)

// Checker validates jobs before they are queued, so a bad input anywhere
// in a batch is caught before any rendering starts.
type Checker struct {
	Tools    ffmpeg.Tools
	Settings Settings
	Series   func(name string) (Series, bool)
}

// Check returns the problems that would stop steps from running on j.
// When trimming and stitching together, the trim's output need not
// exist yet.
func (c Checker) Check(ctx context.Context, j *Job, steps []Step) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	trim, stitch := has(steps, StepTrim), has(steps, StepStitch)

	series, seriesOK := c.Series(j.Series)
	if stitch {
		switch {
		case j.Series == "":
			add("No series is selected.")
		case !seriesOK:
			add("Series %q doesn't exist.", j.Series)
		default:
			for _, p := range []string{series.Intro, series.Outro} {
				if !exists(p) {
					add("Series %q's file %s is missing.", j.Series, p)
				}
			}
		}
	}

	if trim {
		recordingOK := false
		switch {
		case j.Recording == "":
			add("No recording is set.")
		case !exists(j.Recording):
			add("Recording not found: %s", j.Recording)
		default:
			recordingOK = true
		}
		if j.Start == nil || j.End == nil {
			add("The sermon start and end aren't both set.")
		} else if *j.Start < 0 || *j.End <= *j.Start {
			add("The sermon end (%s) must be after its start (%s).", timestamp.Format(*j.End), timestamp.Format(*j.Start))
		} else {
			length := *j.End - *j.Start
			if seriesOK && length <= series.TransitionDuration {
				add("The sermon is only %.1fs, too short for the series' %gs transition.", length, series.TransitionDuration)
			}
			if recordingOK {
				info, err := media.Probe(ctx, c.Tools, j.Recording)
				switch {
				case err != nil:
					add("Couldn't read the recording: %v", err)
				case info.Duration > 0 && *j.End > info.Duration+0.5:
					add("The sermon end (%s) is past the end of the recording (%s).", timestamp.Format(*j.End), timestamp.Format(info.Duration))
				}
			}
		}
	} else if stitch {
		switch {
		case j.Trimmed == "":
			add("The job hasn't been trimmed yet.")
		case !exists(j.Trimmed):
			add("The trimmed clip is missing: %s", j.Trimmed)
		}
	}
	return problems
}

// CheckBatch checks every job, and that no two write the same file.
// Problems are keyed by job ID.
func (c Checker) CheckBatch(ctx context.Context, js []*Job, steps []Step) map[string][]string {
	out := map[string][]string{}
	for _, j := range js {
		if p := c.Check(ctx, j, steps); len(p) > 0 {
			out[j.ID] = p
		}
	}
	type output struct{ label, path string }
	seen := map[string]*Job{}
	for _, j := range js {
		var outs []output
		if has(steps, StepTrim) {
			outs = append(outs, output{"trimmed clip", j.TrimPath(c.Settings)})
		}
		if has(steps, StepStitch) {
			outs = append(outs, output{"final video", j.FinalPath(c.Settings)})
		}
		for _, o := range outs {
			key := pathKey(o.path)
			if other, ok := seen[key]; ok {
				out[j.ID] = append(out[j.ID], fmt.Sprintf("Its %s %s is also written by job %s.", o.label, o.path, other.ID))
				continue
			}
			seen[key] = j
		}
	}
	return out
}

func has(steps []Step, s Step) bool {
	for _, x := range steps {
		if x == s {
			return true
		}
	}
	return false
}

func exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// pathKey compares paths the way Windows does: case-insensitively.
func pathKey(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return strings.ToLower(filepath.Clean(p))
}
