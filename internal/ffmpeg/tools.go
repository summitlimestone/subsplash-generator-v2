// Package ffmpeg finds the bundled ffmpeg/ffprobe binaries and runs them.
package ffmpeg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DevDirEnv overrides the folder the binaries are loaded from. It exists
// for development and tests only; a shipped build always runs the copy
// bundled beside the executable, never one found on PATH.
const DevDirEnv = "SG_FFMPEG_DIR"

// Tools holds the paths of the ffmpeg and ffprobe binaries to run.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// Locate returns the bundled binaries: the "ffmpeg" folder next to the
// executable, or the folder named by DevDirEnv when it is set.
func Locate() (Tools, error) {
	dir := os.Getenv(DevDirEnv)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return Tools{}, fmt.Errorf("finding the program's folder: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir = filepath.Join(filepath.Dir(exe), "ffmpeg")
	}
	return InDir(dir)
}

// InDir returns the binaries in dir, checking that both exist.
func InDir(dir string) (Tools, error) {
	t := Tools{
		FFmpeg:  filepath.Join(dir, exeName("ffmpeg")),
		FFprobe: filepath.Join(dir, exeName("ffprobe")),
	}
	for _, p := range []string{t.FFmpeg, t.FFprobe} {
		if _, err := os.Stat(p); err != nil {
			return Tools{}, fmt.Errorf("bundled ffmpeg is missing: %w", err)
		}
	}
	return t, nil
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
