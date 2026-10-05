package render

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
)

// Encoder is an H.264 encoder backend for full re-encodes.
type Encoder struct {
	Codec         string
	Presets       []string
	DefaultPreset string
	// qualityArgs targets a CRF-like quality (0-51, lower is better).
	qualityArgs func(crf int, preset string) []string
	// bitrateArgs targets an average bitrate in kbps.
	bitrateArgs func(kbps int, preset string) []string
}

// Software is the encoder every other one falls back to.
const Software = "software"

// Encoders are the selectable backends by name.
var Encoders = map[string]Encoder{
	Software: {
		Codec:         "libx264",
		Presets:       []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow", "placebo"},
		DefaultPreset: "veryfast",
		qualityArgs: func(crf int, p string) []string {
			return []string{"-crf", strconv.Itoa(crf), "-preset", p}
		},
		bitrateArgs: func(kbps int, p string) []string {
			return []string{"-b:v", kbpsArg(kbps), "-preset", p}
		},
	},
	"nvenc": {
		// The same number means higher quality on x264 than as NVENC's -cq,
		// so a CRF tuned for software isn't directly comparable here.
		Codec:         "h264_nvenc",
		Presets:       []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"},
		DefaultPreset: "p4",
		qualityArgs: func(crf int, p string) []string {
			return []string{"-rc", "vbr", "-cq", strconv.Itoa(crf), "-preset", p}
		},
		bitrateArgs: func(kbps int, p string) []string {
			return []string{"-rc", "vbr", "-b:v", kbpsArg(kbps), "-preset", p}
		},
	},
	"qsv": {
		Codec:         "h264_qsv",
		Presets:       []string{"veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"},
		DefaultPreset: "veryfast",
		qualityArgs: func(crf int, p string) []string {
			return []string{"-global_quality", strconv.Itoa(crf), "-preset", p}
		},
		bitrateArgs: func(kbps int, p string) []string {
			return []string{"-b:v", kbpsArg(kbps), "-preset", p}
		},
	},
	"amf": {
		// No CRF equivalent; constant QP on every frame type is closest.
		Codec:         "h264_amf",
		Presets:       []string{"speed", "balanced", "quality", "high_quality"},
		DefaultPreset: "speed",
		qualityArgs: func(crf int, p string) []string {
			q := strconv.Itoa(crf)
			return []string{"-rc", "cqp", "-qp_i", q, "-qp_p", q, "-qp_b", q, "-quality", p}
		},
		bitrateArgs: func(kbps int, p string) []string {
			return []string{"-rc", "vbr_latency", "-b:v", kbpsArg(kbps), "-quality", p}
		},
	},
}

// EncoderNames lists the selectable encoders, software first.
func EncoderNames() []string {
	names := slices.Sorted(maps.Keys(Encoders))
	i := slices.Index(names, Software)
	return append([]string{Software}, slices.Delete(names, i, i+1)...)
}

func kbpsArg(kbps int) string { return strconv.Itoa(kbps) + "k" }

// Encode selects how video is re-encoded.
type Encode struct {
	Encoder     string // a key of Encoders; empty means Software
	Preset      string // empty means the encoder's default
	CRF         int
	BitrateKbps int // when set, targets this average bitrate instead of CRF
}

func (e Encode) args(enc Encoder, preset string) []string {
	if e.BitrateKbps > 0 {
		return enc.bitrateArgs(e.BitrateKbps, preset)
	}
	return enc.qualityArgs(e.CRF, preset)
}

// encodeWithFallback runs build(codec, codecArgs) with the chosen encoder,
// retrying with software if a hardware encoder fails to run at all, since
// a missing GPU or driver shouldn't fail the render.
func (r *Runner) encodeWithFallback(ctx context.Context, e Encode, label string, duration float64, build func(codec string, codecArgs []string) []string) error {
	name := e.Encoder
	if name == "" {
		name = Software
	}
	enc, ok := Encoders[name]
	if !ok {
		r.log().Warn("unknown encoder, using software", "step", label, "encoder", name)
		name, enc = Software, Encoders[Software]
	}
	preset := e.Preset
	if preset != "" && !slices.Contains(enc.Presets, preset) {
		r.log().Warn("encoder has no such preset, using its default", "step", label, "encoder", name, "preset", preset)
		preset = ""
	}
	if preset == "" {
		preset = enc.DefaultPreset
	}

	err := r.run(ctx, label, duration, build(enc.Codec, e.args(enc, preset)))
	if err == nil || name == Software || ctx.Err() != nil {
		return err
	}
	r.log().Warn("encoder failed, falling back to software", "step", label, "encoder", name, "err", err)
	sw := Encoders[Software]
	if err2 := r.run(ctx, label, duration, build(sw.Codec, e.args(sw, sw.DefaultPreset))); err2 != nil {
		return fmt.Errorf("%s failed with %s and with the software fallback: %w", label, name, errors.Join(err, err2))
	}
	return nil
}

// run runs one ffmpeg step, reporting its progress as a fraction of duration.
func (r *Runner) run(ctx context.Context, label string, duration float64, args []string) error {
	_, err := r.runStderr(ctx, label, duration, args)
	return err
}

// runStderr is run, also returning the tail of ffmpeg's stderr.
func (r *Runner) runStderr(ctx context.Context, label string, duration float64, args []string) (string, error) {
	r.log().Debug("ffmpeg", "step", label, "args", args)
	return r.Tools.RunStderr(ctx, args, func(p ffmpeg.Progress) {
		if r.OnProgress == nil {
			return
		}
		frac := 1.0
		if !p.Done && duration > 0 {
			frac = math.Min(1, p.OutTime.Seconds()/duration)
		}
		r.OnProgress(Step{Label: label, Fraction: frac, Speed: p.Speed})
	})
}
