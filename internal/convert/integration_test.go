package convert

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"media-converter/internal/ffmpeg"
)

// fixture is a 30 s 48 kHz stereo FLAC. Its non-ASCII name also exercises path
// handling end to end.
const fixture = "../../fixtures/ピンクノイズ-48000-ステレオ-30秒.flac"

// executable is the FFmpeg these tests drive: whatever is on PATH, which is
// what the tool uses too.
func executable(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	return path
}

// setup copies the fixture into a temporary directory under each given name and
// returns the options to convert them, plus the inputs.
func setup(t *testing.T, format string, remove bool, names ...string) (Options, []string) {
	t.Helper()

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}

	root := t.TempDir()
	inputs := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}

	target, err := ffmpeg.Resolve(format, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		FFmpeg:      executable(t),
		Target:      target,
		OutputDir:   filepath.Join(root, "out"),
		Remove:      remove,
		Concurrency: 2,
	}, inputs
}

func run(t *testing.T, ctx context.Context, opts Options, inputs []string) (Summary, []Result) {
	t.Helper()

	jobs, rejected := Plan(inputs, opts)
	if len(rejected) != 0 {
		t.Fatalf("Plan refused %v", rejected)
	}

	var results []Result
	summary := Run(ctx, jobs, opts, func(result Result) { results = append(results, result) })
	return summary, results
}

func TestConversionProducesTheContainerItPromises(t *testing.T) {
	opts, inputs := setup(t, "opus", false, "song.flac")

	_, results := run(t, context.Background(), opts, inputs)
	if results[0].Err != nil {
		t.Fatalf("conversion failed: %v", results[0].Err)
	}

	data, err := os.ReadFile(results[0].Output)
	if err != nil {
		t.Fatal(err)
	}
	// Ogg Opus, and big enough that the encode was not truncated.
	if !bytes.HasPrefix(data, []byte("OggS")) || !bytes.Contains(data[:4096], []byte("OpusHead")) {
		t.Errorf("output is not Ogg Opus: % x", data[:min(16, len(data))])
	}
	if len(data) < 20_000 {
		t.Errorf("output is %d bytes, far below 30 s of audio", len(data))
	}
}

func TestConcurrentRunConvertsEveryFileOnce(t *testing.T) {
	opts, inputs := setup(t, "flac", false, "a.flac", "b.flac", "ピンク.flac", "with space.flac")
	opts.Concurrency = 4

	summary, results := run(t, context.Background(), opts, inputs)
	if summary.Converted != len(inputs) || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want all %d converted", summary, len(inputs))
	}
	if len(results) != len(inputs) {
		t.Errorf("reported %d results, want %d", len(results), len(inputs))
	}
	entries, err := os.ReadDir(opts.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(inputs) {
		t.Errorf("output directory has %d entries, want %d", len(entries), len(inputs))
	}
}

func TestOutputIsInstalledSafelyOrNotAtAll(t *testing.T) {
	// The three guarantees of one conversion: the source survives a failure,
	// no partial output is left behind, and the output carries the source's
	// permissions rather than the 0600 of the temporary file it was written to.
	opts, inputs := setup(t, "flac", true, "good.flac")
	broken := filepath.Join(filepath.Dir(inputs[0]), "broken.flac")
	if err := os.WriteFile(broken, []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inputs[0], 0o640); err != nil {
		t.Fatal(err)
	}

	_, results := run(t, context.Background(), opts, append(inputs, broken))
	byName := map[string]Result{}
	for _, r := range results {
		byName[filepath.Base(r.Input)] = r
	}

	if r := byName["good.flac"]; r.Err != nil || !r.Removed {
		t.Errorf("good.flac: err=%v removed=%v, want success and removal", r.Err, r.Removed)
	}
	if r := byName["broken.flac"]; r.Err == nil || r.Removed {
		t.Errorf("broken.flac: err=%v removed=%v, want failure and no removal", r.Err, r.Removed)
	}
	if _, err := os.Stat(broken); err != nil {
		t.Errorf("a failed conversion deleted its source: %v", err)
	}

	entries, err := os.ReadDir(opts.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("output directory = %v, want only the successful conversion", entries)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(byName["good.flac"].Output)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Errorf("output mode = %v, want the source's 0640", info.Mode().Perm())
		}
	}
}

func TestCancellationConvertsNothingAndKeepsEverything(t *testing.T) {
	opts, inputs := setup(t, "flac", true, "a.flac", "b.flac")
	opts.Concurrency = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	summary, _ := run(t, ctx, opts, inputs)
	// Nothing was attempted, so nothing failed: counting these as failures
	// would claim broken files where the user pressed Ctrl-C.
	if summary.Cancelled != len(inputs) || summary.Failed != 0 || summary.Converted != 0 {
		t.Errorf("summary = %+v, want %d cancelled and nothing else", summary, len(inputs))
	}
	for _, in := range inputs {
		if _, err := os.Stat(in); err != nil {
			t.Errorf("%s was deleted under cancellation: %v", in, err)
		}
	}
}
