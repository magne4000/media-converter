package input

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// library builds a small on-disk music library and returns its root.
func library(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, rel := range []string{
		"artist/album/01 track.flac",
		"artist/album/02 track.wav",
		"artist/album/cover.jpg",
		"artist/single/hit.mp3",
		"loose.flac",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// rel makes results comparable across temporary directories.
func rel(t *testing.T, root string, paths []string) []string {
	t.Helper()

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func TestDiscover(t *testing.T) {
	lossless := []string{"artist/album/01 track.flac", "artist/album/02 track.wav", "loose.flac"}

	tests := []struct {
		name    string
		args    func(root string) []string
		filter  Filter
		want    []string
		wantErr string
	}{
		{
			name: "a directory recurses and keeps only lossless audio",
			args: func(root string) []string { return []string{root} },
			want: lossless,
		},
		{
			name:   "--all-formats admits lossy but never non-audio",
			args:   func(root string) []string { return []string{root} },
			filter: Filter{AllFormats: true},
			want:   append(slices.Clone(lossless), "artist/single/hit.mp3"),
		},
		{
			name: "** is expanded internally, and overlaps are deduplicated",
			args: func(root string) []string {
				return []string{filepath.Join(root, "**", "*.flac"), filepath.Join(root, "loose.flac")}
			},
			want: []string{"artist/album/01 track.flac", "loose.flac"},
		},
		{
			name: "a missing path is reported and the rest kept",
			args: func(root string) []string {
				return []string{filepath.Join(root, "nope.flac"), filepath.Join(root, "loose.flac")}
			},
			want:    []string{"loose.flac"},
			wantErr: "nope.flac",
		},
		{
			name:    "a pattern matching nothing accepted is a typo, not no work",
			args:    func(root string) []string { return []string{filepath.Join(root, "artist/single/*.mp3")} },
			wantErr: "no matching audio files",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := library(t)

			got, problems := Discover(tc.args(root), tc.filter)
			if (len(problems) > 0) != (tc.wantErr != "") {
				t.Fatalf("Discover problems = %v, want %q", problems, tc.wantErr)
			}
			if tc.wantErr != "" && !strings.Contains(problems[0].Error(), tc.wantErr) {
				t.Errorf("problem = %q, want %q", problems[0], tc.wantErr)
			}
			if relative := rel(t, root, got); !slices.Equal(relative, slices.Sorted(slices.Values(tc.want))) {
				t.Errorf("Discover = %v, want %v", relative, tc.want)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	// Lidarr's path: the sources are already enumerated and -i only narrows
	// them. A pattern matching nothing is a no-op here, unlike Discover, so a
	// script configured for FLAC stays quiet on an album of MP3s.
	root := library(t)
	flac := filepath.Join(root, "artist/album/01 track.flac")
	wav := filepath.Join(root, "artist/album/02 track.wav")
	jpg := filepath.Join(root, "artist/album/cover.jpg")
	gone := filepath.Join(root, "artist/album/gone.flac")

	got, problems := Select([]string{wav, flac, jpg, flac}, nil, Filter{})
	if len(problems) != 0 {
		t.Fatalf("Select: %v", problems)
	}
	// Deduplicated, filtered, and sorted so conversion order does not depend
	// on the order Lidarr listed the tracks in.
	if want := []string{"artist/album/01 track.flac", "artist/album/02 track.wav"}; !slices.Equal(rel(t, root, got), want) {
		t.Errorf("Select = %v, want %v", rel(t, root, got), want)
	}

	if got, problems := Select([]string{wav}, []string{"*.flac"}, Filter{}); len(problems) != 0 || len(got) != 0 {
		t.Errorf("Select with a non-matching pattern = %v, %v; want silence", got, problems)
	}
	if _, problems := Select([]string{gone}, nil, Filter{}); len(problems) == 0 {
		t.Error("a vanished track was not reported")
	}
}

func TestFilterAccepts(t *testing.T) {
	tests := []struct {
		path   string
		filter Filter
		want   bool
	}{
		{path: "a.FLAC", want: true}, // case-insensitive
		{path: "cover.jpg", want: false},
		{path: "track.mp3", want: false}, // lossy needs --all-formats
		{path: "track.mp3", filter: Filter{AllFormats: true}, want: true},
		// .m4a carries AAC far more often than ALAC and cannot be told apart
		// by extension, so it is lossy-only.
		{path: "track.m4a", want: false},
		{path: "track.m4a", filter: Filter{AllFormats: true}, want: true},
	}

	for _, tc := range tests {
		if got := tc.filter.Accepts(tc.path); got != tc.want {
			t.Errorf("Filter%+v.Accepts(%q) = %v, want %v", tc.filter, tc.path, got, tc.want)
		}
	}
}

func TestMatchAnyPatterns(t *testing.T) {
	path := filepath.Join("music", "artist", "album", "song.flac")
	for pattern, want := range map[string]bool{
		"*.flac":          true, // no separator: matches the base name
		"*.wav":           false,
		"**/album/*.flac": true, // separator: matches the whole path
		"**/other/*.flac": false,
	} {
		if got := matchAny(path, []string{pattern}); got != want {
			t.Errorf("MatchAny(%q) = %v, want %v", pattern, got, want)
		}
	}
}

func TestSymlinksAndSpecialFiles(t *testing.T) {
	// A symlinked track is a track: naming it, globbing it and walking to it
	// must agree. A FIFO is not, and must never reach FFmpeg, which would
	// wait on it forever.
	root := t.TempDir()
	real := filepath.Join(root, "store.flac")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(root, "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(lib, "linked.flac")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "gone.flac"), filepath.Join(lib, "broken.flac")); err != nil {
		t.Fatal(err)
	}

	got, problems := Discover([]string{lib}, Filter{})
	if len(problems) != 0 {
		t.Fatalf("Discover: %v", problems)
	}
	if want := []string{"linked.flac"}; !slices.Equal(rel(t, lib, got), want) {
		t.Errorf("Discover = %v, want %v", rel(t, lib, got), want)
	}

	pipe := filepath.Join(root, "pipe.flac")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("FIFOs unavailable: %v", err)
	}
	if _, problems := Discover([]string{pipe}, Filter{}); len(problems) == 0 || !strings.Contains(problems[0].Error(), "not a regular file") {
		t.Errorf("Discover(FIFO) = %v, want it reported", problems)
	}
	if got, problems := Select([]string{pipe}, nil, Filter{}); len(problems) != 0 || len(got) != 0 {
		t.Errorf("Select(FIFO) = %v, %v; want it skipped", got, problems)
	}
}
