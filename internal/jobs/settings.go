package jobs

import (
	"os"
	"path/filepath"
)

// Slide identifies a ProPresenter slide that marks the sermon start or end.
type Slide struct {
	UID           string `json:"uid,omitempty"`
	Text          string `json:"text,omitempty"`
	Match         string `json:"match,omitempty"` // "exact" or "regex"
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
}

// Settings are the app's configuration.
type Settings struct {
	TrimmedDir string `json:"trimmed_dir"`
	FinalDir   string `json:"final_dir"`
	// PadStart and PadEnd shift live marks: positive is later.
	PadStart float64 `json:"pad_start"`
	PadEnd   float64 `json:"pad_end"`
	Render   Render  `json:"render"`
	// Backlogs are the backlog folders that have been opened.
	Backlogs []string `json:"backlogs"`
	// Welcomed is set once the first-run welcome is finished or skipped.
	Welcomed bool `json:"welcomed"`

	OBS struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
	} `json:"obs"`
	ProPresenter struct {
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Password   string `json:"password"`
		BeginSlide Slide  `json:"begin_slide"`
		EndSlide   Slide  `json:"end_slide"`
	} `json:"propresenter"`
	API struct {
		Enabled bool   `json:"enabled"`
		Host    string `json:"host"` // 127.0.0.1, or 0.0.0.0 for the LAN
		Port    int    `json:"port"`
		Token   string `json:"token"`
	} `json:"api"`
}

// DefaultSettings are used until the user changes anything.
func DefaultSettings() Settings {
	videos := filepath.Join(home(), "Videos", "Subsplash")
	s := Settings{
		TrimmedDir: filepath.Join(videos, "Trimmed"),
		FinalDir:   videos,
		Render: Render{
			TrimCRF: 18, StitchCRF: 23, Encoder: "nvenc", FastCopy: true,
			Normalize: true, TargetLUFS: -16, Subsplash: true,
		},
	}
	s.OBS.Host, s.OBS.Port = "localhost", 4455
	s.ProPresenter.Port = 1025
	s.API.Host, s.API.Port = "127.0.0.1", 8765
	return s
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// Series is a named intro, outro and transition.
type Series struct {
	Name               string  `json:"name"`
	Intro              string  `json:"intro"`
	IntroDuration      float64 `json:"intro_duration"` // for an image intro
	Outro              string  `json:"outro"`
	OutroDuration      float64 `json:"outro_duration"`
	Transition         string  `json:"transition"`
	TransitionDuration float64 `json:"transition_duration"`
	Hidden             bool    `json:"hidden"`
}
