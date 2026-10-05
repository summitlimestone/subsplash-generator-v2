// Package render trims and stitches service videos with ffmpeg.
package render

import (
	"log/slog"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
)

// Step is a progress report for one ffmpeg step of a render.
type Step struct {
	Label    string
	Fraction float64 // 0 to 1
	Speed    string
}

// Runner runs renders with one set of tools, logging and progress sink.
type Runner struct {
	Tools      ffmpeg.Tools
	Log        *slog.Logger // nil discards
	OnProgress func(Step)   // nil ignores progress
}

func (r *Runner) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

// The AAC settings every render uses unless a preset says otherwise.
var aacArgs = []string{"-c:a", "aac", "-b:a", "192k"}
