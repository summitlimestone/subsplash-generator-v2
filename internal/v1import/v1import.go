// Package v1import reads v1's config.json, series.json, render-state and
// bulk states files into v2 settings, series and jobs.
package v1import

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/timestamp"
)

// Dir is where v1 kept config.json and series.json.
func Dir() string {
	if base := os.Getenv("APPDATA"); base != "" {
		return filepath.Join(base, "subsplash-generator")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "subsplash-generator")
}

type slide struct {
	UID           string `json:"uid"`
	Text          string `json:"text"`
	MatchMode     string `json:"match_mode"`
	CaseSensitive bool   `json:"case_sensitive"`
}

type trimCfg struct {
	Output        *string  `json:"output"`
	PadStart      *float64 `json:"pad_start_seconds"`
	PadEnd        *float64 `json:"pad_end_seconds"`
	CRF           *float64 `json:"crf"`
	FastCopy      *bool    `json:"fast_copy"`
	Normalize     *bool    `json:"normalize_audio"`
	TargetLUFS    *float64 `json:"normalize_target_lufs"`
	Encoder       *string  `json:"encoder"`
	EncoderPreset *string  `json:"encoder_preset"`
}

type stitchCfg struct {
	Series        *string  `json:"series"`
	Output        *string  `json:"output"`
	CRF           *float64 `json:"crf"`
	Subsplash     *bool    `json:"subsplash_preset"`
	Encoder       *string  `json:"encoder"`
	EncoderPreset *string  `json:"encoder_preset"`
}

type config struct {
	API struct {
		Enabled *bool   `json:"enabled"`
		Host    *string `json:"host"`
		Port    *int    `json:"port"`
	} `json:"api"`
	ProPresenter struct {
		Host       *string `json:"host"`
		Port       *int    `json:"port"`
		Password   *string `json:"password"`
		BeginSlide *slide  `json:"begin_slide"`
		EndSlide   *slide  `json:"end_slide"`
	} `json:"propresenter"`
	OBS struct {
		Host     *string `json:"host"`
		Port     *int    `json:"port"`
		Password *string `json:"password"`
	} `json:"obs"`
	Trim   trimCfg   `json:"trim"`
	Stitch stitchCfg `json:"stitch"`
}

// Config applies a v1 config.json on top of s. v1's API password is not
// carried over: v2 uses a generated token instead.
func Config(path string, s jobs.Settings) (jobs.Settings, error) {
	var c config
	if err := readJSON(path, &c); err != nil {
		return s, err
	}
	set(&s.API.Enabled, c.API.Enabled)
	set(&s.API.Host, c.API.Host)
	set(&s.API.Port, c.API.Port)
	set(&s.ProPresenter.Host, c.ProPresenter.Host)
	set(&s.ProPresenter.Port, c.ProPresenter.Port)
	set(&s.ProPresenter.Password, c.ProPresenter.Password)
	if c.ProPresenter.BeginSlide != nil {
		s.ProPresenter.BeginSlide = convertSlide(*c.ProPresenter.BeginSlide)
	}
	if c.ProPresenter.EndSlide != nil {
		s.ProPresenter.EndSlide = convertSlide(*c.ProPresenter.EndSlide)
	}
	set(&s.OBS.Host, c.OBS.Host)
	set(&s.OBS.Port, c.OBS.Port)
	set(&s.OBS.Password, c.OBS.Password)
	set(&s.PadStart, c.Trim.PadStart)
	set(&s.PadEnd, c.Trim.PadEnd)
	s.Render = applyRender(s.Render, c.Trim, c.Stitch)
	if c.Trim.Output != nil {
		if dir := outputDir(*c.Trim.Output); dir != "" {
			s.TrimmedDir = dir
		}
	}
	if c.Stitch.Output != nil {
		if dir := outputDir(*c.Stitch.Output); dir != "" {
			s.FinalDir = dir
		}
	}
	return s, nil
}

func convertSlide(v slide) jobs.Slide {
	if v.UID != "" {
		return jobs.Slide{UID: v.UID}
	}
	if v.Text == "" {
		return jobs.Slide{}
	}
	sl := jobs.Slide{Text: v.Text, Match: "exact", CaseSensitive: v.CaseSensitive}
	switch v.MatchMode {
	case "regex":
		sl.Match = "regex"
	case "substring":
		sl.Match, sl.Text = "regex", regexp.QuoteMeta(v.Text)
	}
	return sl
}

func applyRender(r jobs.Render, t trimCfg, st stitchCfg) jobs.Render {
	if t.CRF != nil {
		r.TrimCRF = int(*t.CRF)
	}
	if st.CRF != nil {
		r.StitchCRF = int(*st.CRF)
	}
	set(&r.FastCopy, t.FastCopy)
	set(&r.Normalize, t.Normalize)
	set(&r.TargetLUFS, t.TargetLUFS)
	set(&r.Subsplash, st.Subsplash)
	// v1 had separate trim and stitch encoders; the stitch one decided
	// the final video, so it wins.
	set(&r.Encoder, t.Encoder)
	set(&r.Encoder, st.Encoder)
	set(&r.Preset, t.EncoderPreset)
	set(&r.Preset, st.EncoderPreset)
	return r
}

var strftimeCode = regexp.MustCompile(`%[YymdHIMSpBbAaj%]`)

// outputDir is the fixed folder part of a v1 output path: everything
// before the first component with a date placeholder, e.g. D:\video for
// D:\video\%Y-%m-%d\body_trimmed.mp4. Relative folders are dropped,
// since v1 resolved them against wherever it happened to run.
func outputDir(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	parts := strings.Split(p, "/")
	keep := parts[:len(parts)-1] // never the file name
	for i, part := range keep {
		if strftimeCode.MatchString(part) {
			keep = keep[:i]
			break
		}
	}
	dir := strings.Join(keep, "/")
	if dir == "" || !isAbs(dir+"/") {
		return ""
	}
	return filepath.FromSlash(dir)
}

// Series reads a v1 series.json.
func Series(path string) ([]jobs.Series, error) {
	var in []jobs.Series // v1 used the same field names
	if err := readJSON(path, &in); err != nil {
		return nil, err
	}
	var out []jobs.Series
	for _, sr := range in {
		if sr.Name == "" {
			continue
		}
		if sr.Transition == "" {
			sr.Transition = "fade"
		}
		if sr.TransitionDuration <= 0 {
			sr.TransitionDuration = 1
		}
		out = append(out, sr)
	}
	return out, nil
}

type state struct {
	Recording   *string         `json:"recording_path"`
	RawBegin    json.RawMessage `json:"raw_begin_offset"`
	RawEnd      json.RawMessage `json:"raw_end_offset"`
	TrimmedPath *string         `json:"trimmed_path"`
	Trim        trimCfg         `json:"trim"`
	Stitch      stitchCfg       `json:"stitch"`
}

// States reads a v1 render-state file (one object) or bulk states file
// (an array) as jobs. Relative paths are resolved against the file's
// folder, as v1's bulk render did.
func States(path string, s jobs.Settings) ([]*jobs.Job, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw = trimBOM(raw)
	var list []state
	if t := strings.TrimSpace(string(raw)); strings.HasPrefix(t, "{") {
		var one state
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
		}
		list = []state{one}
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	base := filepath.Dir(path)
	var out []*jobs.Job
	for i, st := range list {
		j, err := toJob(st, base, s)
		if err != nil {
			return nil, fmt.Errorf("%s entry %d: %w", filepath.Base(path), i+1, err)
		}
		out = append(out, j)
	}
	return out, nil
}

func toJob(st state, base string, s jobs.Settings) (*jobs.Job, error) {
	j := jobs.New(jobs.SourceImport, s)
	j.Render = applyRender(s.Render, st.Trim, st.Stitch)
	if st.Recording != nil {
		j.Recording = resolve(*st.Recording, base)
	}
	if st.Stitch.Series != nil {
		j.Series = *st.Stitch.Series
	}
	begin, okB, err := offset(st.RawBegin)
	if err != nil {
		return nil, fmt.Errorf("raw_begin_offset: %w", err)
	}
	end, okE, err := offset(st.RawEnd)
	if err != nil {
		return nil, fmt.Errorf("raw_end_offset: %w", err)
	}
	if okB && okE {
		// Only the file's own padding applies: watch wrote it alongside
		// live marks, while hand-placed marks (the Sermon Marker's) have none.
		var padStart, padEnd float64
		set(&padStart, st.Trim.PadStart)
		set(&padEnd, st.Trim.PadEnd)
		j.SetMarks(max(0, begin+padStart), max(0, end+padEnd))
	}
	if st.Trim.Output != nil && literal(*st.Trim.Output) {
		j.TrimOutput = resolve(*st.Trim.Output, base)
	}
	if st.Stitch.Output != nil && literal(*st.Stitch.Output) {
		j.FinalOutput = resolve(*st.Stitch.Output, base)
	}
	if st.TrimmedPath != nil && *st.TrimmedPath != "" {
		if p := resolve(*st.TrimmedPath, base); fileExists(p) {
			j.Trimmed = p
		}
	}
	j.Date = guessDate(j)
	j.Status = j.BaseStatus()
	return j, nil
}

// offset reads a v1 offset: "HH:MM:SS.mmm", a number of seconds, or null.
func offset(raw json.RawMessage) (float64, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, false, fmt.Errorf("%s isn't a timestamp", raw)
	}
	v, err := timestamp.Parse(s)
	return v, err == nil, err
}

// guessDate takes the date from a date-named output (the v1 Sermon
// Marker's), else from the recording's file name.
func guessDate(j *jobs.Job) string {
	if d := jobs.DateFromName(j.FinalOutput); d != "" {
		return d
	}
	return jobs.DateFromName(j.Recording)
}

func literal(p string) bool { return p != "" && !strftimeCode.MatchString(p) }

func resolve(p, base string) string {
	if p == "" || isAbs(p) {
		return p
	}
	return filepath.Join(base, filepath.FromSlash(strings.ReplaceAll(p, `\`, "/")))
}

var windowsAbs = regexp.MustCompile(`^([A-Za-z]:[\\/]|\\\\|//)`)

// isAbs treats Windows drive and UNC paths as absolute on any OS, since
// v1 files come from Windows machines.
func isAbs(p string) bool { return filepath.IsAbs(p) || windowsAbs.MatchString(p) }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func set[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(trimBOM(raw), v); err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	return nil
}

func trimBOM(b []byte) []byte { return []byte(strings.TrimPrefix(string(b), "\ufeff")) }
