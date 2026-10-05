package render

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// loudnorm's true-peak ceiling and loudness range; only the integrated
// target is configurable.
const (
	targetTP  = -2.0
	targetLRA = 7.0
)

// loudness is loudnorm's first-pass measurement of a range of audio.
type loudness struct {
	I, TP, LRA, Thresh, Offset string
}

var jsonObjectRE = regexp.MustCompile(`\{[^{}]*\}`)

// measureLoudness runs loudnorm's analysis pass over [start, end] of
// src's audio, so the encode can apply one fixed gain to the whole range
// instead of single-pass loudnorm's continuously adjusted one.
// It returns ok=false (and logs why) when the result is unusable, e.g.
// silent audio, so the trim goes ahead without normalizing.
func (r *Runner) measureLoudness(ctx context.Context, src string, start, end, targetI float64) (loudness, bool, error) {
	stderr, err := r.runStderr(ctx, "measuring loudness", end-start, []string{
		"-ss", fmt.Sprintf("%.3f", start), "-i", src, "-t", fmt.Sprintf("%.3f", end-start),
		"-vn", "-af", fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:print_format=json", targetI, targetTP, targetLRA),
		"-f", "null", "-",
	})
	if ctx.Err() != nil {
		return loudness{}, false, ctx.Err()
	}
	if err != nil {
		r.log().Warn("loudness measurement failed, not normalizing", "err", err)
		return loudness{}, false, nil
	}
	matches := jsonObjectRE.FindAllString(stderr, -1)
	if len(matches) == 0 {
		r.log().Warn("no loudness report from ffmpeg, not normalizing")
		return loudness{}, false, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(matches[len(matches)-1]), &m); err != nil {
		r.log().Warn("unreadable loudness report, not normalizing", "err", err)
		return loudness{}, false, nil
	}
	l := loudness{I: m["input_i"], TP: m["input_tp"], LRA: m["input_lra"], Thresh: m["input_thresh"], Offset: m["target_offset"]}
	for name, v := range map[string]string{"input_i": l.I, "input_tp": l.TP, "input_lra": l.LRA, "input_thresh": l.Thresh, "target_offset": l.Offset} {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			r.log().Warn("audio looks silent, not normalizing", "field", name, "value", v)
			return loudness{}, false, nil
		}
	}
	return l, true, nil
}

// loudnormFilter is loudnorm's second pass using a prior measurement.
func loudnormFilter(targetI float64, m loudness) string {
	return fmt.Sprintf(
		"loudnorm=I=%g:TP=%g:LRA=%g:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true:print_format=summary",
		targetI, targetTP, targetLRA, m.I, m.TP, m.LRA, m.Thresh, m.Offset,
	)
}
