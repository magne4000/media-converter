package cli

import (
	"reflect"
	"strings"
	"testing"

	"media-converter/internal/ffmpeg"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Request
	}{{
		name: "variadic input stops at the next flag, positionals join it",
		args: []string{"-i", "a.flac", "b.wav", "-o", "/out", "trailing.flac"},
		want: Request{Inputs: []string{"a.flac", "b.wav", "trailing.flac"}, OutputDir: "/out"},
	}, {
		name: "forwarded options keep command-line order",
		args: []string{"--b:a=192k", "--q:a=7", "--shortest=", "-i", "a.flac"},
		want: Request{Inputs: []string{"a.flac"}, Options: []ffmpeg.Option{
			{Key: "b:a", Value: "192k"}, {Key: "q:a", Value: "7"}, {Key: "shortest"},
		}},
	}, {
		name: "codec selectors become an override, not a forwarded option",
		args: []string{"--c:a=libopus", "-i", "a.flac"},
		want: Request{Inputs: []string{"a.flac"}, CodecOverride: "libopus"},
	}, {
		name: "short switches cluster and carry attached values",
		args: []string{"-rnc8", "song.flac"},
		want: Request{Inputs: []string{"song.flac"}, Remove: true, DryRun: true, Concurrency: 8},
	}, {
		name: "everything after -- is a path",
		args: []string{"--", "-weird.flac", "--also-weird.flac"},
		want: Request{Inputs: []string{"-weird.flac", "--also-weird.flac"}},
	}, {
		name: "-i repeats, since it accumulates",
		args: []string{"-i", "a.flac", "-i", "b.wav"},
		want: Request{Inputs: []string{"a.flac", "b.wav"}},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.args)
			if err != nil {
				t.Fatalf("Parse(%v): %v", test.args, err)
			}

			want := test.want
			if want.Concurrency == 0 {
				want.Concurrency = DefaultConcurrency
			}
			want.FormatName = DefaultFormat
			if !reflect.DeepEqual(*got, want) {
				t.Errorf("Request = %+v, want %+v", *got, want)
			}
		})
	}
}

func TestParseRejections(t *testing.T) {
	tests := map[string][]string{
		"--key=value":          {"--vn"},
		`unknown option "-z"`:  {"-rz"},
		"takes no value":       {"--remove=yes"},
		"more than once":       {"-f", "ogg", "--format=mp3"},
		"--q:a given more":     {"--q:a=7", "--q:a=5"},
		"positive integer":     {"-c", "0"},
		"requires a value":     {"-f="},
		"at least one path":    {"-i"},
		"not an FFmpeg option": {"----weird=1"},
	}

	for want, args := range tests {
		_, err := Parse(args)
		if err == nil {
			t.Errorf("Parse(%v) = nil error, want %q", args, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%v) error = %q, want %q", args, err, want)
		}
	}
}

func TestUsageDocumentsEveryOption(t *testing.T) {
	usage := Usage("test")

	for spelling := range known {
		if !strings.Contains(usage, spelling) {
			t.Errorf("usage does not document %q", spelling)
		}
	}
	for _, line := range strings.Split(usage, "\n") {
		if len(line) > 80 {
			t.Errorf("usage line exceeds 80 columns: %q", line)
		}
	}
}
