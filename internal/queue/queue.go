// Package queue renders jobs one step at a time, in order, with live
// jobs ahead of everything else.
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/render"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
)

// Event reports a change: a job's new state, with Progress set while a
// step is running.
type Event struct {
	Job      *jobs.Job
	Progress *render.Step
}

// Queue runs queued render steps on a single worker.
type Queue struct {
	store   *store.Store
	tools   ffmpeg.Tools
	log     *slog.Logger
	onEvent func(Event)

	mu      sync.Mutex
	pending []item
	running *item
	cancel  context.CancelFunc
	wake    chan struct{}
	idle    *sync.Cond
}

type item struct {
	jobID string
	steps []jobs.Step // remaining, in order
	live  bool
}

// New returns a queue. onEvent, if non-nil, is called for every change,
// from the worker goroutine.
func New(st *store.Store, tools ffmpeg.Tools, log *slog.Logger, onEvent func(Event)) *Queue {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	q := &Queue{store: st, tools: tools, log: log, onEvent: onEvent, wake: make(chan struct{}, 1)}
	q.idle = sync.NewCond(&q.mu)
	return q
}

// ValidationError lists why jobs can't be queued, keyed by job ID.
type ValidationError map[string][]string

func (e ValidationError) Error() string {
	n := 0
	for _, p := range e {
		n += len(p)
	}
	return fmt.Sprintf("%d job(s) have %d problem(s); nothing was queued", len(e), n)
}

// Add validates the jobs and queues steps for each of them. If any job
// has a problem, nothing is queued and a ValidationError is returned.
func (q *Queue) Add(ctx context.Context, ids []string, steps []jobs.Step) error {
	if len(steps) == 0 {
		return errors.New("no steps to run")
	}
	steps = slices.Clone(steps)
	slices.SortFunc(steps, func(a, b jobs.Step) int { return stepOrder(a) - stepOrder(b) })
	set, err := q.store.Settings()
	if err != nil {
		return err
	}
	var js []*jobs.Job
	for _, id := range ids {
		j, err := q.store.Job(id)
		if err != nil {
			return fmt.Errorf("job %s: %w", id, err)
		}
		if q.isQueued(id) {
			return fmt.Errorf("job %s is already queued", id)
		}
		js = append(js, j)
	}
	checker := jobs.Checker{Tools: q.tools, Settings: set, Series: func(name string) (jobs.Series, bool) {
		sr, err := q.store.FindSeries(name)
		return sr, err == nil
	}}
	if problems := checker.CheckBatch(ctx, js, steps); len(problems) > 0 {
		return ValidationError(problems)
	}

	for _, j := range js {
		updated, err := q.store.UpdateJob(j.ID, func(j *jobs.Job) {
			j.Status, j.Error = jobs.StatusQueued, ""
		})
		if err != nil {
			return err
		}
		q.emit(Event{Job: updated})
		q.mu.Lock()
		it := item{jobID: j.ID, steps: steps, live: j.Source == jobs.SourceLive}
		if it.live {
			// Ahead of everything not live, behind earlier live jobs.
			i := slices.IndexFunc(q.pending, func(p item) bool { return !p.live })
			if i < 0 {
				i = len(q.pending)
			}
			q.pending = slices.Insert(q.pending, i, it)
		} else {
			q.pending = append(q.pending, it)
		}
		q.mu.Unlock()
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}

func stepOrder(s jobs.Step) int {
	if s == jobs.StepTrim {
		return 0
	}
	return 1
}

func (q *Queue) isQueued(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running != nil && q.running.jobID == id {
		return true
	}
	return slices.ContainsFunc(q.pending, func(p item) bool { return p.jobID == id })
}

// Cancel removes a job from the queue, stopping it if it is rendering.
func (q *Queue) Cancel(id string) {
	q.mu.Lock()
	n := len(q.pending)
	q.pending = slices.DeleteFunc(q.pending, func(p item) bool { return p.jobID == id })
	removed := len(q.pending) < n
	if q.running != nil && q.running.jobID == id && q.cancel != nil {
		q.cancel()
	}
	q.mu.Unlock()
	if removed {
		if j, err := q.store.UpdateJob(id, func(j *jobs.Job) { j.Status = j.BaseStatus() }); err == nil {
			q.emit(Event{Job: j})
		}
	}
}

// Wait blocks until nothing is queued or running.
func (q *Queue) Wait() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.running != nil || len(q.pending) > 0 {
		q.idle.Wait()
	}
}

// Run works through the queue until ctx is cancelled.
func (q *Queue) Run(ctx context.Context) {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.idle.Broadcast()
			q.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
				continue
			}
		}
		it := q.pending[0]
		q.pending = q.pending[1:]
		stepCtx, cancel := context.WithCancel(ctx)
		q.running, q.cancel = &it, cancel
		q.mu.Unlock()

		q.runItem(stepCtx, it)
		cancel()

		q.mu.Lock()
		q.running, q.cancel = nil, nil
		q.idle.Broadcast()
		q.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}

func (q *Queue) runItem(ctx context.Context, it item) {
	for _, step := range it.steps {
		err := q.runStep(ctx, it.jobID, step)
		if err == nil {
			continue
		}
		cancelled := errors.Is(err, context.Canceled)
		j, uerr := q.store.UpdateJob(it.jobID, func(j *jobs.Job) {
			if cancelled {
				j.Status, j.Error = j.BaseStatus(), "cancelled"
			} else {
				j.Status, j.Error = jobs.StatusFailed, err.Error()
			}
		})
		if uerr != nil {
			q.log.Error("saving job", "job", it.jobID, "err", uerr)
			return
		}
		q.log.Warn("render step failed", "job", it.jobID, "step", step, "err", err)
		q.emit(Event{Job: j})
		return
	}
	// A job queued for a trim alone rests at "trimmed" for checking.
}

func (q *Queue) runStep(ctx context.Context, id string, step jobs.Step) error {
	set, err := q.store.Settings()
	if err != nil {
		return err
	}
	working := jobs.StatusTrimming
	if step == jobs.StepStitch {
		working = jobs.StatusStitching
	}
	j, err := q.store.UpdateJob(id, func(j *jobs.Job) { j.Status, j.Error = working, "" })
	if err != nil {
		return err
	}
	q.emit(Event{Job: j})

	r := &render.Runner{Tools: q.tools, Log: q.log.With("job", id), OnProgress: func(s render.Step) {
		q.emit(Event{Job: j, Progress: &s})
	}}
	enc := render.Encode{Encoder: j.Render.Encoder, Preset: j.Render.Preset}

	switch step {
	case jobs.StepTrim:
		out := j.TrimPath(set)
		enc.CRF = j.Render.TrimCRF
		err = r.Trim(ctx, j.Recording, out, render.Trim{
			Start: *j.Start, End: *j.End, FastCopy: j.Render.FastCopy, Encode: enc,
			Normalize: j.Render.Normalize, TargetLUFS: j.Render.TargetLUFS,
		})
		if err != nil {
			return err
		}
		j, err = q.store.UpdateJob(id, func(j *jobs.Job) { j.Trimmed, j.Status = out, jobs.StatusTrimmed })
	case jobs.StepStitch:
		var sr jobs.Series
		if sr, err = q.store.FindSeries(j.Series); err != nil {
			return fmt.Errorf("series %q: %w", j.Series, err)
		}
		enc.CRF = j.Render.StitchCRF
		err = r.Stitch(ctx, j.Trimmed, j.FinalPath(set), render.Stitch{
			Intro:      render.Clip{Path: sr.Intro, ImageDuration: sr.IntroDuration},
			Outro:      render.Clip{Path: sr.Outro, ImageDuration: sr.OutroDuration},
			Transition: sr.Transition, TransitionDuration: sr.TransitionDuration,
			Encode: enc, Subsplash: j.Render.Subsplash,
		})
		if err != nil {
			return err
		}
		j, err = q.store.UpdateJob(id, func(j *jobs.Job) { j.Status = jobs.StatusDone })
	}
	if err != nil {
		return err
	}
	q.emit(Event{Job: j})
	return nil
}

func (q *Queue) emit(e Event) {
	if q.onEvent != nil {
		q.onEvent(e)
	}
}
