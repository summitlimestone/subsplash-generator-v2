package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
)

// Trim configures cutting a recording down to [Start, End] seconds.
type Trim struct {
	Start, End float64
	// FastCopy re-encodes only up to the first keyframe after Start and
	// stream-copies the rest, falling back to a full re-encode whenever
	// that isn't possible or doesn't verify.
	FastCopy bool
	Encode   Encode // for the full re-encode
	// Normalize loudness-normalizes the audio to TargetLUFS.
	Normalize  bool
	TargetLUFS float64
}

// fastCopyEndMargin pulls a stream copy's end back by this many frames:
// -c copy cuts at packet boundaries and tends to round the end up.
const fastCopyEndMargin = 1.5

// errNoFastCopy means fast copy doesn't apply; the caller re-encodes.
var errNoFastCopy = errors.New("fast copy not possible")

// Trim writes [o.Start, o.End] of src to dst. dst is removed on failure.
func (r *Runner) Trim(ctx context.Context, src, dst string, o Trim) (err error) {
	if o.Start < 0 || o.End <= o.Start {
		return fmt.Errorf("invalid trim range %.3fs to %.3fs", o.Start, o.End)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()

	var audioFilter []string
	if o.Normalize {
		m, ok, err := r.measureLoudness(ctx, src, o.Start, o.End, o.TargetLUFS)
		if err != nil {
			return err
		}
		if ok {
			audioFilter = []string{"-af", loudnormFilter(o.TargetLUFS, m)}
		}
	}

	if o.FastCopy {
		err := r.fastCopyTrim(ctx, src, dst, o, audioFilter)
		if err == nil || ctx.Err() != nil {
			return err
		}
		r.log().Info("fast copy not used, re-encoding the whole trim", "reason", err)
	}

	duration := o.End - o.Start
	return r.encodeWithFallback(ctx, o.Encode, "trimming", duration, func(codec string, codecArgs []string) []string {
		// -ss before -i seeks fast, and is still frame-accurate when re-encoding.
		args := []string{
			"-y", "-ss", fmt.Sprintf("%.3f", o.Start), "-i", src, "-t", fmt.Sprintf("%.3f", duration),
			"-map", "0:v:0", "-map", "0:a:0?", "-c:v", codec,
		}
		args = append(args, codecArgs...)
		args = append(args, aacArgs...)
		args = append(args, audioFilter...)
		return append(args, dst)
	})
}

func (r *Runner) fastCopyTrim(ctx context.Context, src, dst string, o Trim, audioFilter []string) error {
	info, err := media.Probe(ctx, r.Tools, src)
	if err != nil {
		return err
	}
	codec, ok := media.CopyCodecs[info.VideoCodec]
	if !ok {
		return fmt.Errorf("%w: source codec %q isn't supported", errNoFastCopy, info.VideoCodec)
	}
	kf, ok, err := media.FindNextKeyframe(ctx, r.Tools, src, o.Start, codec)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: no safe keyframe after %.3fs", errNoFastCopy, o.Start)
	}
	if kf >= o.End {
		return fmt.Errorf("%w: the trim is shorter than one keyframe interval", errNoFastCopy)
	}
	tailDuration := max(0, o.End-kf-fastCopyEndMargin/info.FPS)
	copyArgs := func(out string) []string {
		args := []string{
			"-y", "-ss", fmt.Sprintf("%.3f", kf), "-i", src, "-t", fmt.Sprintf("%.3f", tailDuration),
			"-map", "0:v:0", "-map", "0:a:0?", "-c:v", "copy",
		}
		args = append(args, aacArgs...)
		args = append(args, audioFilter...)
		args = append(args, timescaleArgs...)
		return append(args, out)
	}

	if kf-o.Start <= 0.02 {
		// Already on a keyframe: nothing needs re-encoding.
		return r.run(ctx, "copying", tailDuration, copyArgs(dst))
	}

	base := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst))
	sliver, tail := base+".sliver"+filepath.Ext(dst), base+".tail"+filepath.Ext(dst)
	defer os.Remove(sliver)
	defer os.Remove(tail)

	sliverArgs := []string{
		"-y", "-ss", fmt.Sprintf("%.3f", o.Start), "-i", src, "-t", fmt.Sprintf("%.3f", kf-o.Start),
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", codec.Encoder, "-crf", fmt.Sprint(o.Encode.CRF), "-preset", "veryfast",
	}
	sliverArgs = append(sliverArgs, codec.ExtraArgs...)
	sliverArgs = append(sliverArgs, aacArgs...)
	sliverArgs = append(sliverArgs, audioFilter...)
	sliverArgs = append(sliverArgs, timescaleArgs...)
	if err := r.run(ctx, "re-encoding the start", kf-o.Start, append(sliverArgs, sliver)); err != nil {
		return err
	}
	if err := r.run(ctx, "copying", tailDuration, copyArgs(tail)); err != nil {
		return err
	}
	return r.concat(ctx, []string{sliver, tail}, dst, []float64{kf - o.Start}, kf-o.Start+tailDuration)
}
