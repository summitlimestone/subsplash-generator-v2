package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/summitlimestone/subsplash-generator-v2/internal/appdir"
	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/queue"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
	"github.com/summitlimestone/subsplash-generator-v2/internal/timestamp"
	"github.com/summitlimestone/subsplash-generator-v2/internal/v1import"
)

// dataFlag adds -data and returns a function opening the store it names.
func dataFlag(fs *flag.FlagSet) func() (*store.Store, error) {
	dir := fs.String("data", "", "data folder (default: the app's)")
	return func() (*store.Store, error) {
		d := *dir
		if d == "" {
			var err error
			if d, err = appdir.Data(); err != nil {
				return nil, err
			}
		}
		return store.Open(appdir.Database(d))
	}
}

func importCmd(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	open := dataFlag(fs)
	v1 := fs.Bool("v1", false, "import v1's config.json and series.json from "+v1import.Dir())
	config := fs.String("config", "", "a v1 config.json to import")
	series := fs.String("series", "", "a v1 series.json to import")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *v1 {
		if *config == "" {
			*config = filepath.Join(v1import.Dir(), "config.json")
		}
		if *series == "" {
			*series = filepath.Join(v1import.Dir(), "series.json")
		}
	}
	if *config == "" && *series == "" && fs.NArg() == 0 {
		return errors.New("usage: sg import [-v1] [-config F] [-series F] [STATE_FILE...]")
	}
	st, err := open()
	if err != nil {
		return err
	}
	defer st.Close()
	if *config != "" {
		set, err := st.Settings()
		if err != nil {
			return err
		}
		if set, err = v1import.Config(*config, set); err != nil {
			return err
		}
		if err := st.SaveSettings(set); err != nil {
			return err
		}
		fmt.Printf("imported settings from %s\n", *config)
	}
	if *series != "" {
		list, err := v1import.Series(*series)
		if err != nil {
			return err
		}
		for _, sr := range list {
			if err := st.SaveSeries(sr); err != nil {
				return err
			}
		}
		fmt.Printf("imported %d series from %s\n", len(list), *series)
	}
	for _, f := range fs.Args() {
		js, err := importStates(st, f)
		if err != nil {
			return err
		}
		fmt.Printf("imported %d job(s) from %s\n", len(js), f)
	}
	return nil
}

func importStates(st *store.Store, path string) ([]*jobs.Job, error) {
	set, err := st.Settings()
	if err != nil {
		return nil, err
	}
	js, err := v1import.States(path, set)
	if err != nil {
		return nil, err
	}
	for _, j := range js {
		if err := st.SaveJob(j); err != nil {
			return nil, err
		}
	}
	return js, nil
}

func jobsCmd(args []string) error {
	fs := flag.NewFlagSet("jobs", flag.ContinueOnError)
	open := dataFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := open()
	if err != nil {
		return err
	}
	defer st.Close()
	all, err := st.Jobs()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tNAME\tSERIES\tSTART\tEND\tRECORDING")
	for _, j := range all {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.Status, j.Stem, j.Series, mark(j.Start), mark(j.End), filepath.Base(j.Recording))
		if j.Error != "" {
			fmt.Fprintf(w, "\t  error: %s\n", j.Error)
		}
	}
	return w.Flush()
}

func mark(v *float64) string {
	if v == nil {
		return "-"
	}
	return timestamp.Format(*v)
}

func renderCmd(ctx context.Context, tools ffmpeg.Tools, args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	open := dataFlag(fs)
	steps := fs.String("steps", "trim,stitch", "steps to run: trim, stitch, or both")
	all := fs.Bool("all", false, "render every job")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := open()
	if err != nil {
		return err
	}
	defer st.Close()
	ids := fs.Args()
	if *all {
		js, err := st.Jobs()
		if err != nil {
			return err
		}
		ids = nil
		for _, j := range js {
			ids = append(ids, j.ID)
		}
	}
	if len(ids) == 0 {
		return errors.New("usage: sg render [-steps trim,stitch] (-all | ID...)")
	}
	return renderJobs(ctx, st, tools, ids, *steps)
}

func bulkCmd(ctx context.Context, tools ffmpeg.Tools, args []string) error {
	fs := flag.NewFlagSet("bulk", flag.ContinueOnError)
	open := dataFlag(fs)
	steps := fs.String("steps", "trim,stitch", "steps to run: trim, stitch, or both")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: sg bulk [-steps trim,stitch] STATES_FILE")
	}
	st, err := open()
	if err != nil {
		return err
	}
	defer st.Close()
	js, err := importStates(st, fs.Arg(0))
	if err != nil {
		return err
	}
	var ids []string
	for _, j := range js {
		ids = append(ids, j.ID)
	}
	fmt.Printf("imported %d job(s)\n", len(ids))
	err = renderJobs(ctx, st, tools, ids, *steps)
	var ve queue.ValidationError
	if errors.As(err, &ve) {
		// Nothing ran, so don't leave the batch behind to be imported twice.
		for _, id := range ids {
			_ = st.DeleteJob(id)
		}
	}
	return err
}

func parseSteps(s string) ([]jobs.Step, error) {
	var out []jobs.Step
	for _, part := range strings.Split(s, ",") {
		switch step := jobs.Step(strings.TrimSpace(part)); step {
		case jobs.StepTrim, jobs.StepStitch:
			out = append(out, step)
		default:
			return nil, fmt.Errorf("unknown step %q", part)
		}
	}
	return out, nil
}

// renderJobs validates, queues and renders ids, reporting progress, and
// fails if any job fails.
func renderJobs(ctx context.Context, st *store.Store, tools ffmpeg.Tools, ids []string, stepList string) error {
	steps, err := parseSteps(stepList)
	if err != nil {
		return err
	}
	if _, err := st.RecoverInterrupted(); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	last := ""
	q := queue.New(st, tools, log, func(e queue.Event) {
		if e.Progress != nil {
			line := fmt.Sprintf("%s %s %3.0f%% %s", e.Job.Stem, e.Progress.Label, e.Progress.Fraction*100, e.Progress.Speed)
			if line != last {
				fmt.Fprintf(os.Stderr, "\r%-70s", line)
				last = line
			}
			return
		}
		fmt.Fprintf(os.Stderr, "\r%-70s\n", fmt.Sprintf("%s: %s %s", e.Job.Stem, e.Job.Status, e.Job.Error))
	})
	if err := q.Add(ctx, ids, steps); err != nil {
		var ve queue.ValidationError
		if errors.As(err, &ve) {
			keys := make([]string, 0, len(ve))
			for id := range ve {
				keys = append(keys, id)
			}
			sort.Strings(keys)
			for _, id := range keys {
				for _, p := range ve[id] {
					fmt.Fprintf(os.Stderr, "job %s: %s\n", id, p)
				}
			}
		}
		return err
	}
	go q.Run(ctx)
	q.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failed := 0
	for _, id := range ids {
		if j, err := st.Job(id); err == nil && j.Status == jobs.StatusFailed {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d job(s) failed", failed, len(ids))
	}
	fmt.Printf("%d job(s) rendered\n", len(ids))
	return nil
}
