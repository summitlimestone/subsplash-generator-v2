package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
)

// DefaultImageDuration is how long a still-image intro or outro shows
// when no duration is given.
const DefaultImageDuration = 5.0

// Clip is an intro or outro: a video, or a still image shown for
// ImageDuration seconds.
type Clip struct {
	Path          string
	ImageDuration float64 // images only; zero means DefaultImageDuration
}

// Stitch configures crossfading an intro, the trimmed body and an outro.
type Stitch struct {
	Intro, Outro       Clip
	Transition         string  // an xfade transition name; empty means "fade"
	TransitionDuration float64 // seconds
	Encode             Encode
	// Subsplash forces Subsplash's recommended On-Demand 1080p settings:
	// 1920x1080 at 30 fps, High@4.0, 2400 kbps, keyint 60, AAC 160 kbps.
	Subsplash bool
}

var subsplash1080p = struct {
	width, height, fps, keyint, videoKbps, audioKbps int
	profile, level                                   string
}{1920, 1080, 30, 60, 2400, 160, "high", "4.0"}

type stitchInput struct {
	path     string
	image    bool
	duration float64
	hasAudio bool
}

// Stitch crossfades o.Intro, main and o.Outro into dst. dst is removed
// on failure.
func (r *Runner) Stitch(ctx context.Context, main, dst string, o Stitch) (err error) {
	if o.Transition == "" {
		o.Transition = "fade"
	}
	if o.TransitionDuration <= 0 {
		return fmt.Errorf("transition duration must be positive")
	}
	if media.IsImage(main) {
		return fmt.Errorf("the main clip must be a video, not a still image")
	}

	mainInfo, err := media.Probe(ctx, r.Tools, main)
	if err != nil {
		return err
	}
	inputs := make([]stitchInput, 3)
	for i, c := range []Clip{o.Intro, {Path: main}, o.Outro} {
		if c.Path == "" {
			return fmt.Errorf("intro and outro are both required")
		}
		in := stitchInput{path: c.Path}
		switch {
		case i == 1:
			in.duration, in.hasAudio = mainInfo.Duration, mainInfo.HasAudio
		case media.IsImage(c.Path):
			if _, _, err := media.ProbeImage(ctx, r.Tools, c.Path); err != nil {
				return err
			}
			in.image, in.duration = true, c.ImageDuration
			if in.duration <= 0 {
				in.duration = DefaultImageDuration
			}
		default:
			info, err := media.Probe(ctx, r.Tools, c.Path)
			if err != nil {
				return err
			}
			in.duration, in.hasAudio = info.Duration, info.HasAudio
		}
		name := [3]string{"intro", "main clip", "outro"}[i]
		if in.duration == 0 {
			return fmt.Errorf("couldn't read the %s's duration (is the file complete?)", name)
		}
		if in.duration <= o.TransitionDuration {
			return fmt.Errorf("the %s is only %.2fs, too short for a %gs transition", name, in.duration, o.TransitionDuration)
		}
		inputs[i] = in
	}

	width, height, fps := mainInfo.Width, mainInfo.Height, mainInfo.FPS
	if o.Subsplash {
		width, height, fps = subsplash1080p.width, subsplash1080p.height, float64(subsplash1080p.fps)
	}
	// yuv420p needs even dimensions.
	width -= width % 2
	height -= height % 2

	args := []string{"-y"}
	for _, in := range inputs {
		if in.image {
			args = append(args, "-loop", "1", "-t", fmt.Sprintf("%.3f", in.duration))
		}
		args = append(args, "-i", in.path)
	}
	// Clips without audio get a silent track so the crossfade has three.
	audio := make([]string, 3)
	next := 3
	for i, in := range inputs {
		if in.hasAudio {
			audio[i] = fmt.Sprintf("%d:a", i)
			continue
		}
		args = append(args, "-f", "lavfi", "-t", fmt.Sprintf("%.3f", in.duration),
			"-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
		audio[i] = fmt.Sprintf("%d:a", next)
		next++
	}
	args = append(args, "-filter_complex", stitchFilter(inputs, audio, width, height, fps, o.Transition, o.TransitionDuration),
		"-map", "[vout]", "-map", "[aout]")

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()

	encode := o.Encode
	if o.Subsplash {
		encode.BitrateKbps = subsplash1080p.videoKbps
	}
	total := inputs[0].duration + inputs[1].duration + inputs[2].duration - 2*o.TransitionDuration
	return r.encodeWithFallback(ctx, encode, "stitching", total, func(codec string, codecArgs []string) []string {
		cmd := append(append([]string{}, args...), "-c:v", codec)
		cmd = append(cmd, codecArgs...)
		if o.Subsplash {
			s := subsplash1080p
			// No +faststart: Subsplash's own preset leaves it off.
			return append(cmd, "-profile:v", s.profile, "-level:v", s.level, "-g", strconv.Itoa(s.keyint),
				"-c:a", "aac", "-b:a", kbpsArg(s.audioKbps), dst)
		}
		cmd = append(cmd, aacArgs...)
		return append(cmd, "-movflags", "+faststart", dst)
	})
}

// stitchFilter scales every input to one size and frame rate, then
// chains two crossfades: intro into main, then that into the outro.
func stitchFilter(in []stitchInput, audio []string, width, height int, fps float64, transition string, d float64) string {
	var parts []string
	for i := range 3 {
		parts = append(parts, fmt.Sprintf(
			"[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%g,format=yuv420p[v%d]",
			i, width, height, width, height, fps, i))
	}
	// xfade can output 4:4:4, which a High-profile encode rejects, so
	// re-force yuv420p after each one.
	parts = append(parts,
		fmt.Sprintf("[v0][v1]xfade=transition=%s:duration=%g:offset=%.3f,format=yuv420p[v01]", transition, d, in[0].duration-d),
		fmt.Sprintf("[v01][v2]xfade=transition=%s:duration=%g:offset=%.3f,format=yuv420p[vout]", transition, d, in[0].duration+in[1].duration-2*d),
		fmt.Sprintf("[%s][%s]acrossfade=d=%g[a01]", audio[0], audio[1], d),
		fmt.Sprintf("[a01][%s]acrossfade=d=%g[aout]", audio[2], d),
	)
	return strings.Join(parts, ";")
}
