// Package jobs defines the work the app does: a job is one recording to
// trim and stitch, whether it came from a live service, a single file, a
// backlog or an import.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"path/filepath"
	"time"
)

// Source records how a job was created.
type Source string

const (
	SourceLive    Source = "live"
	SourceFile    Source = "file"
	SourceBacklog Source = "backlog"
	SourceImport  Source = "import"
)

// Status is where a job is in its life.
type Status string

const (
	StatusDraft     Status = "draft"  // missing a recording or marks
	StatusReady     Status = "ready"  // can be trimmed
	StatusQueued    Status = "queued" // waiting in the render queue
	StatusTrimming  Status = "trimming"
	StatusTrimmed   Status = "trimmed" // trim done; check it, then stitch
	StatusStitching Status = "stitching"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
)

// Render holds how a job is encoded. New jobs copy the defaults from Settings.
type Render struct {
	TrimCRF    int     `json:"trim_crf"`
	StitchCRF  int     `json:"stitch_crf"`
	Encoder    string  `json:"encoder"`
	Preset     string  `json:"preset"`
	FastCopy   bool    `json:"fast_copy"`
	Normalize  bool    `json:"normalize"`
	TargetLUFS float64 `json:"target_lufs"`
	Subsplash  bool    `json:"subsplash"`
}

// Job is one recording to trim and stitch.
type Job struct {
	ID        string `json:"id"`
	Source    Source `json:"source"`
	Recording string `json:"recording"`
	// Start and End are seconds into the recording, padding included.
	// Nil means not set yet.
	Start *float64 `json:"start"`
	End   *float64 `json:"end"`
	Date  string   `json:"date"` // YYYY-MM-DD; names the outputs
	// Stem is the output file stem: the date, the date with a _2-style
	// suffix when another job has that date, or the ID with no date.
	Stem   string `json:"stem"`
	Series string `json:"series"`
	// TrimOutput and FinalOutput override the paths derived from Stem.
	TrimOutput  string `json:"trim_output,omitempty"`
	FinalOutput string `json:"final_output,omitempty"`
	Render      Render `json:"render"`
	Trimmed     string `json:"trimmed"` // the trim's output once it succeeds
	// Backlog is the backlog folder the recording belongs to, if any.
	Backlog string `json:"backlog,omitempty"`
	// Skipped marks a backlog recording with no sermon to cut.
	Skipped bool      `json:"skipped,omitempty"`
	Status  Status    `json:"status"`
	Error   string    `json:"error"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// New returns a draft job with a fresh ID and the default render settings.
func New(source Source, s Settings) *Job {
	now := time.Now()
	j := &Job{ID: newID(), Source: source, Render: s.Render, Created: now, Updated: now}
	j.Stem = j.ID
	j.Status = j.BaseStatus()
	return j
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// BaseStatus is the status a job at rest has given its fields.
func (j *Job) BaseStatus() Status {
	switch {
	case j.Recording == "" || j.Start == nil || j.End == nil:
		return StatusDraft
	case j.Trimmed != "":
		return StatusTrimmed
	}
	return StatusReady
}

// SetMarks sets the trim range, to the millisecond. Any earlier trim was
// of the old range, so it no longer counts.
func (j *Job) SetMarks(start, end float64) {
	start, end = math.Round(start*1000)/1000, math.Round(end*1000)/1000
	j.Start, j.End = &start, &end
	j.Trimmed = ""
	j.Status = j.BaseStatus()
}

// TrimPath is where the trim is written.
func (j *Job) TrimPath(s Settings) string {
	if j.TrimOutput != "" {
		return j.TrimOutput
	}
	return filepath.Join(s.TrimmedDir, j.Stem+"_trimmed.mp4")
}

// FinalPath is where the stitched video is written.
func (j *Job) FinalPath(s Settings) string {
	if j.FinalOutput != "" {
		return j.FinalOutput
	}
	return filepath.Join(s.FinalDir, j.Stem+".mp4")
}
