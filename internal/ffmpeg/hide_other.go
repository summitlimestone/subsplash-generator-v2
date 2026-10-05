//go:build !windows

package ffmpeg

import "os/exec"

// HideWindow keeps a console window from flashing up on Windows.
func HideWindow(*exec.Cmd) {}
