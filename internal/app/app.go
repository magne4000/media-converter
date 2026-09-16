package app

import (
	"context"
	"fmt"
	"io"

	"media-converter/internal/cli"
	"media-converter/internal/convert"
	"media-converter/internal/ffmpeg"
	"media-converter/internal/input"
	"media-converter/internal/lidarr"
)

// Env is everything a run depends on outside its own code.
type Env struct {
	Args    []string
	Getenv  func(string) string
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
}

// Run executes one invocation and returns the error that decides the exit
// status. Failures also reach Stderr as they happen, so a partly successful run
// names every input it could not convert.
func Run(ctx context.Context, env Env) error {
	report := reporter{progress: env.Stdout, failures: env.Stderr}

	request, err := cli.Parse(env.Args)
	if err != nil {
		return err
	}
	switch {
	case request.Help:
		fmt.Fprint(env.Stdout, cli.Usage(env.Version))
		return nil
	case request.Version:
		report.progressf("media-converter %s", env.Version)
		return nil
	}

	event := lidarr.Read(env.Getenv)
	if event.IsTest() {
		report.progressf("media-converter: Lidarr connection test OK")
		return nil
	}

	target, err := ffmpeg.Resolve(request.FormatName, request.CodecOverride, request.Options)
	if err != nil {
		return err
	}

	sources, unusable := sources(request, event)
	for _, problem := range unusable {
		report.failuref("ERROR: %v", problem)
	}
	if len(sources) == 0 {
		return nothingToDo(report, event, len(unusable))
	}

	executable, err := ffmpeg.Locate(request.FFmpegPath, env.Getenv)
	if err != nil {
		return err
	}
	report.progressf("media-converter: using %s", executable)

	options := convert.Options{
		FFmpeg:      executable,
		Target:      target,
		OutputDir:   destination(request, event),
		Remove:      request.Remove,
		Concurrency: request.Concurrency,
	}

	jobs, rejected := convert.Plan(sources, options)
	for _, rejection := range rejected {
		report.failuref("ERROR: %v", rejection.Err)
	}

	// A refusal and an unusable input are the same thing to the user: named,
	// not attempted, counted against the exit status.
	refused := len(rejected) + len(unusable)
	inputs := len(sources) + len(unusable)

	if request.DryRun {
		plan(report, jobs, options)
		return outcome(convert.Summary{}, refused, inputs)
	}

	summary := convert.Run(ctx, jobs, options, func(result convert.Result) {
		switch {
		case result.Err != nil:
			report.failuref("ERROR: %s: %v", result.Input, result.Err)
		case result.Removed:
			report.progressf("%s -> %s (source removed)", result.Input, result.Output)
		default:
			report.progressf("%s -> %s", result.Input, result.Output)
		}
	})

	report.progressf("media-converter: %s", tally(summary, refused))
	return outcome(summary, refused, inputs)
}

// sources produces the files to convert, plus one error per input that could
// not be used. Those errors are reported and counted, and the run carries on
// with what it has.
//
// The two modes read -i differently. In CLI mode it names sources, expanded
// from files, directories and globs. In Lidarr mode the sources are the
// imported tracks and -i filters them, which is why a Lidarr script can be
// configured once with `-i '*.flac'` and left alone.
func sources(request *cli.Request, event lidarr.Event) ([]string, []error) {
	filter := input.Filter{AllFormats: request.AllFormats}

	if event.Active() {
		return input.Select(event.TrackPaths, request.Inputs, filter)
	}
	return input.Discover(request.Inputs, filter)
}

// destination is the output directory: beside each input in Lidarr mode, the
// working directory otherwise.
func destination(request *cli.Request, event lidarr.Event) string {
	switch {
	case request.OutputDir != "":
		return request.OutputDir
	case event.Active():
		return ""
	default:
		return "."
	}
}

func nothingToDo(report reporter, event lidarr.Event, unusable int) error {
	switch {
	case unusable > 0:
		return outcome(convert.Summary{}, unusable, unusable)
	case event.Active():
		report.progressf("media-converter: no matching tracks in this Lidarr event")
		return nil
	default:
		return cli.ErrNoInput
	}
}

// plan prints what a real run would do, command line included, since checking
// the options that will reach FFmpeg is the point of --dry-run.
func plan(report reporter, jobs []convert.Job, options convert.Options) {
	for _, job := range jobs {
		report.progressf("%s -> %s", job.Input, job.Output)
		report.progressf("  %s", convert.Command(options, job))
		if options.Remove {
			report.progressf("  then remove %s", job.Input)
		}
	}
	report.progressf("media-converter: dry run, %s planned, nothing written", count(len(jobs), "conversion"))
}

// tally is the one-line summary. Refusals are counted apart from failures: a
// refusal happened before FFmpeg ran, and is usually a wrong -o, -f or path.
func tally(summary convert.Summary, refused int) string {
	line := fmt.Sprintf("%d converted, %d failed", summary.Converted, summary.Failed)
	if summary.Cancelled > 0 {
		line += fmt.Sprintf(", %d cancelled", summary.Cancelled)
	}
	if refused > 0 {
		line += fmt.Sprintf(", %d refused", refused)
	}
	return line
}

func outcome(summary convert.Summary, refused, inputs int) error {
	unconverted := summary.Failed + summary.Cancelled + refused
	if unconverted == 0 {
		return nil
	}
	return fmt.Errorf("%d of %s could not be converted", unconverted, count(inputs, "input"))
}

func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// reporter routes output to the stream its level belongs on.
type reporter struct {
	progress io.Writer
	failures io.Writer
}

func (r reporter) progressf(format string, args ...any) {
	fmt.Fprintf(r.progress, format+"\n", args...)
}
func (r reporter) failuref(format string, args ...any) { fmt.Fprintf(r.failures, format+"\n", args...) }
