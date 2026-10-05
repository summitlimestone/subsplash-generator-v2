//go:build windows

package ffmpeg

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// HideWindow keeps a console window from flashing up for each run.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
