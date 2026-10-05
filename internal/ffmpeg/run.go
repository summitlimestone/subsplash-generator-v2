package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Progress is one of ffmpeg's -progress reports.
type Progress struct {
	OutTime time.Duration // how much output has been written
	Speed   string        // e.g. "4.2x"; empty until ffmpeg reports one
	Done    bool          // the final report
}

// Error is a failed ffmpeg or ffprobe run.
type Error struct {
	Tool   string
	Err    error
	Stderr string // the tail of its stderr
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("%s failed: %v", e.Tool, e.Err)
	}
	return fmt.Sprintf("%s failed: %v: %s", e.Tool, e.Err, msg)
}

func (e *Error) Unwrap() error { return e.Err }

const stderrTail = 16 << 10

// Run runs ffmpeg with args (which must include -y when overwriting),
// calling onProgress, if non-nil, as it reports progress. It returns
// ctx.Err() if ctx is cancelled, after ffmpeg has been killed.
func (t Tools) Run(ctx context.Context, args []string, onProgress func(Progress)) error {
	_, err := t.RunStderr(ctx, args, onProgress)
	return err
}

// RunStderr is Run, also returning the tail of ffmpeg's stderr on
// success, where analysis filters such as loudnorm print their results.
func (t Tools) RunStderr(ctx context.Context, args []string, onProgress func(Progress)) (string, error) {
	full := append([]string{"-hide_banner", "-nostdin", "-nostats", "-progress", "pipe:1"}, args...)
	cmd := t.command(ctx, t.FFmpeg, full)
	stderr := &tailBuffer{max: stderrTail}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", &Error{Tool: "ffmpeg", Err: err}
	}
	readProgress(stdout, onProgress)
	err = cmd.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", &Error{Tool: "ffmpeg", Err: err, Stderr: stderr.String()}
	}
	return stderr.String(), nil
}

// Output runs ffmpeg with args and returns its full stderr, where ffmpeg
// writes analysis results such as trace_headers. Only for short runs.
func (t Tools) Output(ctx context.Context, args []string) (string, error) {
	full := append([]string{"-hide_banner", "-nostdin", "-nostats"}, args...)
	cmd := t.command(ctx, t.FFmpeg, full)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return stderr.String(), &Error{Tool: "ffmpeg", Err: err, Stderr: tail(stderr.String())}
	}
	return stderr.String(), nil
}

// Probe runs ffprobe with args and returns its stdout.
func (t Tools) Probe(ctx context.Context, args []string) ([]byte, error) {
	cmd := t.command(ctx, t.FFprobe, append([]string{"-v", "error"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, &Error{Tool: "ffprobe", Err: err, Stderr: tail(stderr.String())}
	}
	return stdout.Bytes(), nil
}

func (t Tools) command(ctx context.Context, bin string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	// Don't hang on Wait if a killed process left a pipe open.
	cmd.WaitDelay = 5 * time.Second
	hideWindow(cmd)
	return cmd
}

func readProgress(r io.Reader, onProgress func(Progress)) {
	var p Progress
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "out_time_us":
			if us, err := strconv.ParseInt(value, 10, 64); err == nil && us >= 0 {
				p.OutTime = time.Duration(us) * time.Microsecond
			}
		case "speed":
			if value != "N/A" {
				p.Speed = value
			}
		case "progress":
			p.Done = value == "end"
			if onProgress != nil {
				onProgress(p)
			}
		}
	}
	// Drain anything left so ffmpeg never blocks writing to a full pipe.
	_, _ = io.Copy(io.Discard, r)
}

func tail(s string) string {
	if len(s) > stderrTail {
		return s[len(s)-stderrTail:]
	}
	return s
}

// tailBuffer keeps only the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.buf) }

// IsError reports whether err came from a failed ffmpeg or ffprobe run.
func IsError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}
