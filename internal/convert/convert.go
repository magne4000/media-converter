// Package convert turns a list of audio files into converted ones, in
// parallel.
//
// Two properties drive it. An output becomes visible only complete: FFmpeg
// writes a temporary file in the destination directory that is renamed into
// place on success. And a source is deleted only once its replacement carries
// its permissions and holds its final name.
package convert

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"media-converter/internal/ffmpeg"
)

// Options configures a run.
type Options struct {
	FFmpeg      string
	Target      ffmpeg.Target
	OutputDir   string
	Remove      bool
	Concurrency int
}

// Job is a conversion Plan accepted: an input, where it goes, and the
// permissions the output inherits.
type Job struct {
	Input  string
	Output string
	Mode   fs.FileMode
}

// Result is the outcome of one job.
type Result struct {
	Job
	Err     error
	Removed bool
}

// Rejection is an input refused before any work started, and why.
type Rejection struct {
	Input string
	Err   error
}

// Summary counts a finished run. Cancelled jobs are not failures: the run was
// interrupted before they were attempted.
type Summary struct {
	Converted int
	Failed    int
	Cancelled int
}

// Plan pairs each input with its output, refusing every pairing that would
// cost audio: an input converted over itself, two inputs converging on one
// output, and an output that is another input. Paths are compared
// case-insensitively, because on macOS and Windows names differing only in
// case are one file.
func Plan(inputs []string, opts Options) ([]Job, []Rejection) {
	var (
		jobs     []Job
		rejected []Rejection
		sources  = make(map[string]fs.FileInfo, len(inputs))
		claimed  = make(map[string]Job, len(inputs))
	)

	refuse := func(input string, err error) {
		rejected = append(rejected, Rejection{Input: input, Err: err})
	}

	for _, input := range inputs {
		info, err := os.Stat(input)
		if err != nil {
			refuse(input, err)
			continue
		}
		sources[key(input)] = info
	}

	for _, input := range inputs {
		info, listed := sources[key(input)]
		if !listed {
			continue
		}

		output := outputFor(input, opts)
		switch other, taken := claimed[key(output)]; {
		case sameFile(info, output):
			refuse(input, fmt.Errorf("%s: converting it would overwrite it; choose -o or a different -f", input))
		case sources[key(output)] != nil:
			refuse(input, fmt.Errorf("%s: its output %s is itself an input; choose -o or a different -f", input, output))
		case taken:
			refuse(input, fmt.Errorf("%s and %s both convert to %s", other.Input, input, output))
		default:
			job := Job{Input: input, Output: output, Mode: info.Mode().Perm()}
			claimed[key(output)] = job
			jobs = append(jobs, job)
		}
	}
	return jobs, rejected
}

// outputFor keeps the input's base name and replaces its extension. A dotfile
// such as ".flac" has no stem, so the whole name is kept.
func outputFor(input string, opts Options) string {
	base := filepath.Base(input)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}

	dir := opts.OutputDir
	if dir == "" {
		dir = filepath.Dir(input)
	}
	return filepath.Join(dir, stem+opts.Target.Format.Ext)
}

// key identifies a file across the spellings a command line can produce:
// relative against absolute, and differing in case.
func key(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = filepath.Clean(path)
	}
	return strings.ToLower(absolute)
}

func sameFile(input fs.FileInfo, output string) bool {
	existing, err := os.Stat(output)
	return err == nil && os.SameFile(input, existing)
}

// Run converts every job, at most Concurrency at a time, reporting each
// outcome as it lands.
func Run(ctx context.Context, jobs []Job, opts Options, report func(Result)) Summary {
	if opts.Concurrency < 1 {
		panic("convert: concurrency must be positive")
	}

	pending := make(chan Job)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		summary Summary
	)

	for range min(opts.Concurrency, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range pending {
				result := convert(ctx, job, opts)

				mu.Lock()
				summary.count(result.Err)
				report(result)
				mu.Unlock()
			}
		}()
	}

	dispatched := 0
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			close(pending)
			wg.Wait()
			summary.Cancelled += len(jobs) - dispatched
			return summary
		case pending <- job:
			dispatched++
		}
	}
	close(pending)
	wg.Wait()
	return summary
}

func (s *Summary) count(err error) {
	switch {
	case err == nil:
		s.Converted++
	case errors.Is(err, context.Canceled):
		s.Cancelled++
	default:
		s.Failed++
	}
}

func convert(ctx context.Context, job Job, opts Options) Result {
	result := Result{Job: job}

	if err := os.MkdirAll(filepath.Dir(job.Output), 0o755); err != nil {
		result.Err = err
		return result
	}

	temporary, err := os.CreateTemp(filepath.Dir(job.Output), ".media-converter-*"+opts.Target.Format.Ext)
	if err != nil {
		result.Err = err
		return result
	}
	temporary.Close()
	defer os.Remove(temporary.Name())

	if result.Err = ffmpeg.Run(ctx, opts.FFmpeg, opts.Target.Args(job.Input, temporary.Name())); result.Err != nil {
		return result
	}
	// The mode is set before the rename, so the output never holds its final
	// name with the 0600 of a temporary file.
	if result.Err = os.Chmod(temporary.Name(), job.Mode); result.Err != nil {
		return result
	}
	if result.Err = os.Rename(temporary.Name(), job.Output); result.Err != nil {
		return result
	}

	if opts.Remove {
		if err := os.Remove(job.Input); err != nil {
			// The output is already in place, so this is reported rather than
			// treated as a failed conversion.
			result.Err = fmt.Errorf("removing the converted source: %w", err)
			return result
		}
		result.Removed = true
	}
	return result
}

// Command is what a job would run, for --dry-run. It writes straight to the
// destination, where a real run writes a temporary file and renames it.
func Command(opts Options, job Job) string {
	return ffmpeg.Quote(append([]string{opts.FFmpeg}, opts.Target.Args(job.Input, job.Output)...))
}
