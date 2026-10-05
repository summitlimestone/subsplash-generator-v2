package render

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// recordingHandler stores each log message with its attributes.
type recordingHandler struct{ rec *logRecorder }

func newTestLogger(rec *logRecorder) *slog.Logger { return slog.New(recordingHandler{rec}) }

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	h.rec.mu.Lock()
	h.rec.msgs = append(h.rec.msgs, b.String())
	h.rec.mu.Unlock()
	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }
