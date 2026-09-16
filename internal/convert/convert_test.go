package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"media-converter/internal/ffmpeg"
)

func options(t *testing.T, format, outputDir string) Options {
	t.Helper()

	target, err := ffmpeg.Resolve(format, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return Options{FFmpeg: "ffmpeg", Target: target, OutputDir: outputDir, Concurrency: 1}
}

// write creates a file under root and returns its path.
func write(t *testing.T, root, name string) string {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanNamesTheOutput(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name, input, outputDir, want string
	}{
		{name: "beside the input", input: "album/song.flac", want: "album/song.ogg"},
		{name: "into an output directory", input: "album/song.flac", outputDir: "out", want: "out/song.ogg"},
		{name: "spaces and non-ASCII survive", input: "ピンクノイズ 48000.flac", want: "ピンクノイズ 48000.ogg"},
		// A dotfile has no stem, so the whole name is kept.
		{name: "dotfile keeps its name", input: ".flac", want: ".flac.ogg"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := write(t, root, test.input)
			outputDir := test.outputDir
			if outputDir != "" {
				outputDir = filepath.Join(root, outputDir)
			}

			jobs, rejected := Plan([]string{input}, options(t, "ogg", outputDir))
			if len(rejected) != 0 {
				t.Fatalf("refused: %v", rejected[0].Err)
			}
			if want := filepath.Join(root, test.want); jobs[0].Output != want {
				t.Errorf("output = %q, want %q", jobs[0].Output, want)
			}
			if jobs[0].Mode != 0o644 {
				t.Errorf("mode = %v, want the source's 0644", jobs[0].Mode)
			}
		})
	}
}

func TestPlanRefusesWhatWouldCostAudio(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	tests := []struct {
		name     string
		inputs   []string
		format   string
		wantJobs int
		want     string
	}{{
		name:   "an input converted over itself",
		inputs: []string{write(t, root, "song.flac")},
		format: "flac",
		want:   "converting it would overwrite it",
	}, {
		name:     "two inputs converging on one output",
		inputs:   []string{write(t, root, "a/song.flac"), write(t, root, "b/song.wav")},
		format:   "ogg",
		wantJobs: 1,
		want:     "both convert to",
	}, {
		// On macOS and Windows these are one file.
		name:     "outputs differing only in case",
		inputs:   []string{write(t, root, "c/Song.flac"), write(t, root, "d/song.wav")},
		format:   "ogg",
		wantJobs: 1,
		want:     "both convert to",
	}, {
		name:   "an output that is another input, absolute against relative",
		inputs: []string{write(t, root, "pair.flac"), write(t, root, "pair.opus")},
		format: "opus",
		want:   "is itself an input",
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// The output directory is relative where the inputs are absolute,
			// as it is when -o comes from a command line.
			jobs, rejected := Plan(test.inputs, options(t, test.format, "."))
			if len(jobs) != test.wantJobs {
				t.Errorf("jobs = %v, want %d", jobs, test.wantJobs)
			}
			if len(rejected) == 0 {
				t.Fatalf("nothing refused, want %q", test.want)
			}
			if !strings.Contains(rejected[0].Err.Error(), test.want) {
				t.Errorf("reason = %q, want %q", rejected[0].Err, test.want)
			}
		})
	}
}

func TestCommandIsShellReady(t *testing.T) {
	root := t.TempDir()
	input := write(t, root, "don't stop.flac")

	jobs, _ := Plan([]string{input}, options(t, "ogg", root))
	command := Command(options(t, "ogg", root), jobs[0])

	if !strings.Contains(command, `'`+root+`/don'\''t stop.flac'`) {
		t.Errorf("Command = %s", command)
	}
}
