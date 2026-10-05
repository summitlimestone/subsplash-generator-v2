// Command sg runs the render pipeline from the command line:
//
//	sg probe FILE
//	sg trim [flags] SRC DST
//	sg stitch [flags] MAIN DST
//	sg import [-v1] [-config F] [-series F] [STATE_FILE...]
//	sg jobs
//	sg render [-steps trim,stitch] (-all | ID...)
//	sg bulk [-steps trim,stitch] STATES_FILE
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/media"
	"github.com/summitlimestone/subsplash-generator-v2/internal/render"
	"github.com/summitlimestone/subsplash-generator-v2/internal/timestamp"
)

const usage = `usage:
  sg probe FILE
  sg trim [flags] SRC DST
  sg stitch [flags] MAIN DST
  sg import [-v1] [-config F] [-series F] [STATE_FILE...]
  sg jobs
  sg render [-steps trim,stitch] (-all | ID...)
  sg bulk [-steps trim,stitch] STATES_FILE

Run "sg COMMAND -h" for a command's flags.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sg:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch args[0] {
	case "import":
		return importCmd(args[1:])
	case "jobs":
		return jobsCmd(args[1:])
	}
	tools, err := ffmpeg.Locate()
	if err != nil {
		return err
	}
	switch args[0] {
	case "probe":
		return probe(ctx, tools, args[1:])
	case "trim":
		return trim(ctx, tools, args[1:])
	case "stitch":
		return stitch(ctx, tools, args[1:])
	case "render":
		return renderCmd(ctx, tools, args[1:])
	case "bulk":
		return bulkCmd(ctx, tools, args[1:])
	}
	return errors.New(usage)
}

func probe(ctx context.Context, tools ffmpeg.Tools, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: sg probe FILE")
	}
	info, err := media.Probe(ctx, tools, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("duration %s\nsize     %dx%d\nfps      %g\ncodec    %s\naudio    %v\n",
		timestamp.Format(info.Duration), info.Width, info.Height, info.FPS, info.VideoCodec, info.HasAudio)
	return nil
}

// encodeFlags adds the flags shared by trim and stitch.
func encodeFlags(fs *flag.FlagSet) *render.Encode {
	e := &render.Encode{}
	fs.StringVar(&e.Encoder, "encoder", render.Software, "encoder: "+strings.Join(render.EncoderNames(), ", "))
	fs.StringVar(&e.Preset, "preset", "", "encoder preset (default: the encoder's own)")
	fs.IntVar(&e.CRF, "crf", 23, "quality, 0-51, lower is better")
	return e
}

func newRunner(tools ffmpeg.Tools, verbose bool) *render.Runner {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	r := &render.Runner{Tools: tools, Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))}
	last := ""
	r.OnProgress = func(s render.Step) {
		line := fmt.Sprintf("%s %3.0f%% %s", s.Label, s.Fraction*100, s.Speed)
		if line != last {
			fmt.Fprintf(os.Stderr, "\r%-60s", line)
			last = line
		}
		if s.Fraction >= 1 {
			fmt.Fprintln(os.Stderr)
		}
	}
	return r
}

func trim(ctx context.Context, tools ffmpeg.Tools, args []string) error {
	fs := flag.NewFlagSet("trim", flag.ContinueOnError)
	start := fs.String("start", "", "start time, HH:MM:SS.mmm or seconds (required)")
	end := fs.String("end", "", "end time, HH:MM:SS.mmm or seconds (required)")
	fast := fs.Bool("fast-copy", true, "re-encode only up to the first keyframe")
	norm := fs.Bool("normalize", true, "loudness-normalize the audio")
	lufs := fs.Float64("lufs", -16, "loudness target in LUFS")
	verbose := fs.Bool("v", false, "log ffmpeg commands")
	enc := encodeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: sg trim -start T -end T [flags] SRC DST")
	}
	s, err := parseTime(*start)
	if err != nil {
		return fmt.Errorf("-start: %w", err)
	}
	e, err := parseTime(*end)
	if err != nil {
		return fmt.Errorf("-end: %w", err)
	}
	return newRunner(tools, *verbose).Trim(ctx, fs.Arg(0), fs.Arg(1), render.Trim{
		Start: s, End: e, FastCopy: *fast, Encode: *enc, Normalize: *norm, TargetLUFS: *lufs,
	})
}

func stitch(ctx context.Context, tools ffmpeg.Tools, args []string) error {
	fs := flag.NewFlagSet("stitch", flag.ContinueOnError)
	intro := fs.String("intro", "", "intro video or image (required)")
	outro := fs.String("outro", "", "outro video or image (required)")
	introDur := fs.Float64("intro-duration", 0, "seconds to show an image intro")
	outroDur := fs.Float64("outro-duration", 0, "seconds to show an image outro")
	transition := fs.String("transition", "fade", "xfade transition")
	dur := fs.Float64("d", 1, "transition duration in seconds")
	subsplash := fs.Bool("subsplash", false, "use Subsplash's 1080p upload settings")
	verbose := fs.Bool("v", false, "log ffmpeg commands")
	enc := encodeFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: sg stitch -intro F -outro F [flags] MAIN DST")
	}
	return newRunner(tools, *verbose).Stitch(ctx, fs.Arg(0), fs.Arg(1), render.Stitch{
		Intro: render.Clip{Path: *intro, ImageDuration: *introDur}, Outro: render.Clip{Path: *outro, ImageDuration: *outroDur},
		Transition: *transition, TransitionDuration: *dur, Encode: *enc, Subsplash: *subsplash,
	})
}

func parseTime(s string) (float64, error) {
	if s == "" {
		return 0, errors.New("required")
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, nil
	}
	return timestamp.Parse(s)
}
