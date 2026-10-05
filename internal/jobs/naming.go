package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ParseDate reports whether s is a real YYYY-MM-DD date.
func ParseDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// AssignStem picks j's output stem: its date, or the date with _2, _3...
// when another job already uses that stem or a file from an earlier
// render is in the way, or its ID when it has no date. j keeps its
// current stem whenever that is still valid for its date.
func AssignStem(j *Job, others []*Job, s Settings) {
	if j.Date == "" {
		j.Stem = j.ID
		return
	}
	taken := map[string]bool{}
	for _, o := range others {
		if o.ID != j.ID {
			taken[o.Stem] = true
		}
	}
	free := func(stem string) bool {
		if taken[stem] {
			return false
		}
		probe := *j
		probe.Stem = stem
		for _, p := range []string{probe.TrimPath(s), probe.FinalPath(s)} {
			if _, err := os.Stat(p); err == nil {
				return false
			}
		}
		return true
	}
	if stemFor(j.Stem, j.Date) && !taken[j.Stem] {
		return
	}
	for n := 1; ; n++ {
		stem := j.Date
		if n > 1 {
			stem = fmt.Sprintf("%s_%d", j.Date, n)
		}
		if free(stem) {
			j.Stem = stem
			return
		}
	}
}

// stemFor reports whether stem is date or date with a numeric suffix.
func stemFor(stem, date string) bool {
	if stem == date {
		return true
	}
	var n int
	_, err := fmt.Sscanf(stem, date+"_%d", &n)
	return err == nil && n > 1 && stem == fmt.Sprintf("%s_%d", date, n)
}

var dateInName = regexp.MustCompile(`(?:^|\D)(\d{4})[-_. ]?(\d{2})[-_. ]?(\d{2})(?:\D|$)`)

// DateFromName finds a date in a file name, as OBS names recordings
// ("2026-10-04 09-57-37.mkv"), or returns "".
func DateFromName(path string) string {
	if path == "" {
		return ""
	}
	name := filepath.Base(strings.ReplaceAll(path, `\`, "/"))
	for _, m := range dateInName.FindAllStringSubmatch(name, -1) {
		if d := m[1] + "-" + m[2] + "-" + m[3]; ParseDate(d) {
			return d
		}
	}
	return ""
}
