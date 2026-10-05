// Package media reads facts about media files via ffprobe.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
)

// Info describes a video file.
type Info struct {
	// Duration in seconds. Zero when ffprobe can't report one, which
	// happens for a recording that is still being written.
	Duration   float64
	Width      int
	Height     int
	FPS        float64
	VideoCodec string
	HasAudio   bool
}

type probeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
}

// Probe reads a video file's duration, size, frame rate and streams.
func Probe(ctx context.Context, t ffmpeg.Tools, path string) (Info, error) {
	out, err := t.Probe(ctx, []string{
		"-show_entries", "format=duration",
		"-show_entries", "stream=codec_type,codec_name,width,height,r_frame_rate,avg_frame_rate",
		"-of", "json", path,
	})
	if err != nil {
		return Info{}, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	var p probeOutput
	if err := json.Unmarshal(out, &p); err != nil {
		return Info{}, fmt.Errorf("reading %s: unexpected ffprobe output: %w", filepath.Base(path), err)
	}
	var info Info
	if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil && d > 0 {
		info.Duration = d
	}
	found := false
	for _, s := range p.Streams {
		switch s.CodecType {
		case "audio":
			info.HasAudio = true
		case "video":
			if found {
				continue
			}
			found = true
			info.Width, info.Height, info.VideoCodec = s.Width, s.Height, s.CodecName
			// r_frame_rate is the declared rate; avg_frame_rate is derived
			// from timestamps and drifts with them, so it is only a fallback.
			info.FPS = ParseFrameRate(s.RFrameRate)
			if info.FPS == 0 {
				info.FPS = ParseFrameRate(s.AvgFrameRate)
			}
		}
	}
	if !found {
		return Info{}, fmt.Errorf("%s has no video stream", filepath.Base(path))
	}
	if info.FPS == 0 {
		return Info{}, fmt.Errorf("couldn't read %s's frame rate", filepath.Base(path))
	}
	return info, nil
}

// ParseFrameRate reads an ffprobe rate like "30000/1001", returning 0
// for a missing, zero or malformed one such as "0/0".
func ParseFrameRate(s string) float64 {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok || r.Sign() <= 0 {
		return 0
	}
	f, _ := r.Float64()
	return f
}

// ProbeImage reads a still image's size.
func ProbeImage(ctx context.Context, t ffmpeg.Tools, path string) (width, height int, err error) {
	out, err := t.Probe(ctx, []string{
		"-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "json", path,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	var p probeOutput
	if err := json.Unmarshal(out, &p); err != nil || len(p.Streams) == 0 || p.Streams[0].Width == 0 {
		return 0, 0, fmt.Errorf("%s isn't a readable image", filepath.Base(path))
	}
	return p.Streams[0].Width, p.Streams[0].Height, nil
}

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".bmp": true, ".tif": true, ".tiff": true, ".webp": true,
}

// IsImage reports whether path looks like a still image by its extension.
func IsImage(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}
