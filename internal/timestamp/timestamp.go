// Package timestamp converts between seconds and HH:MM:SS.mmm text.
package timestamp

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var pattern = regexp.MustCompile(`^(\d+):([0-5]\d):([0-5]\d)(\.\d+)?$`)

// Format renders seconds as HH:MM:SS.mmm, rounded to the millisecond.
func Format(seconds float64) string {
	ms := int64(math.Round(seconds * 1000))
	sign := ""
	if ms < 0 {
		sign, ms = "-", -ms
	}
	h, ms := ms/3_600_000, ms%3_600_000
	m, ms := ms/60_000, ms%60_000
	s, ms := ms/1000, ms%1000
	return fmt.Sprintf("%s%02d:%02d:%02d.%03d", sign, h, m, s, ms)
}

// Parse reads HH:MM:SS(.fraction), optionally negative, as seconds.
func Parse(text string) (float64, error) {
	text = strings.TrimSpace(text)
	body, negative := strings.CutPrefix(text, "-")
	m := pattern.FindStringSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("%q is not a HH:MM:SS.mmm timestamp", text)
	}
	h, _ := strconv.Atoi(m[1])
	mins, _ := strconv.Atoi(m[2])
	s, _ := strconv.Atoi(m[3])
	total := float64(h*3600 + mins*60 + s)
	if m[4] != "" {
		frac, _ := strconv.ParseFloat("0"+m[4], 64)
		total += frac
	}
	if negative {
		total = -total
	}
	return total, nil
}
