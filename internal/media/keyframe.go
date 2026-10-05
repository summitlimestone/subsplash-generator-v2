package media

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
)

// CopyCodec describes a codec fast-copy trimming can work with: how to
// re-encode a sliver that matches it, and which NAL unit types are safe
// places to start a stream copy.
type CopyCodec struct {
	Encoder   string
	ExtraArgs []string
	// VCL reports whether a NAL type carries a coded picture.
	VCL func(nalType int) bool
	// Safe NAL types are IDR pictures. Open-GOP CRA pictures look like
	// keyframes but their leading pictures reference frames before the
	// cut, so a copy starting there fails to decode.
	Safe map[int]bool
}

// CopyCodecs are the source codecs fast copy supports, by ffprobe name.
var CopyCodecs = map[string]CopyCodec{
	"h264": {
		Encoder: "libx264",
		VCL:     func(n int) bool { return n >= 1 && n <= 5 },
		Safe:    map[int]bool{5: true},
	},
	"hevc": {
		Encoder:   "libx265",
		ExtraArgs: []string{"-tag:v", "hvc1"},
		VCL:       func(n int) bool { return n >= 0 && n < 32 },
		Safe:      map[int]bool{19: true, 20: true},
	},
}

// FindNextKeyframe returns the time of the first safe keyframe at or
// after start, widening the search window if none turns up, or ok=false
// if there is none within the widest window.
func FindNextKeyframe(ctx context.Context, t ffmpeg.Tools, path string, start float64, codec CopyCodec) (at float64, ok bool, err error) {
	for _, window := range []float64{30, 300} {
		// -skip_frame nokey decodes keyframes only, and -read_intervals
		// seeks straight to start, so this stays fast on long files.
		out, err := t.Probe(ctx, []string{
			"-select_streams", "v:0", "-skip_frame", "nokey",
			"-read_intervals", fmt.Sprintf("%.3f%%+%.0f", start, window),
			"-show_entries", "frame=pts_time", "-of", "csv=p=0", path,
		})
		if err != nil {
			return 0, false, err
		}
		var times []float64
		for _, tok := range strings.Fields(string(out)) {
			if v, err := strconv.ParseFloat(strings.TrimRight(tok, ","), 64); err == nil {
				times = append(times, v)
			}
		}
		sort.Float64s(times)
		for _, kf := range times {
			if kf < start-0.001 {
				continue
			}
			safe, err := isSafeKeyframe(ctx, t, path, kf, codec)
			if err != nil {
				return 0, false, err
			}
			if safe {
				return kf, true, nil
			}
		}
	}
	return 0, false, nil
}

var nalTypeRE = regexp.MustCompile(`nal_unit_type\s+\S+\s*=\s*(\d+)\s*$`)

// isSafeKeyframe checks the first coded picture at keyframe time at by
// parsing NAL headers with trace_headers; nothing is decoded.
func isSafeKeyframe(ctx context.Context, t ffmpeg.Tools, path string, at float64, codec CopyCodec) (bool, error) {
	stderr, err := t.Output(ctx, []string{
		"-loglevel", "info", "-ss", fmt.Sprintf("%.3f", at), "-i", path, "-t", "0.2",
		"-c:v", "copy", "-an", "-bsf:v", "trace_headers", "-f", "null", "-",
	})
	if err != nil && ctx.Err() != nil {
		return false, err
	}
	for _, line := range strings.Split(stderr, "\n") {
		m := nalTypeRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if codec.VCL(n) {
			return codec.Safe[n], nil
		}
	}
	return false, nil
}
