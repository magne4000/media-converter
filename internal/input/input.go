// Package input turns command-line arguments into the audio files to convert.
//
// Globbing happens here rather than in the shell, so a quoted '**/*.flac'
// behaves the same on every platform.
package input

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Accepted sources. Lossless is the default set; --all-formats adds the lossy
// one. ".m4a" is lossy-only: it carries AAC far more often than ALAC, and the
// two cannot be told apart by extension.
var (
	lossless = extensions(".flac .wav .wave .w64 .aif .aiff .aifc .alac .ape .wv .tta .shn .dsf .dff")
	lossy    = extensions(".mp3 .m4a .m4b .mp4 .aac .adts .ogg .oga .opus .spx .wma .mp2 .mpc" +
		" .ac3 .eac3 .dts .amr .au .caf .mka .oma .ra")
)

func extensions(list string) map[string]bool {
	set := make(map[string]bool)
	for _, ext := range strings.Fields(list) {
		set[ext] = true
	}
	return set
}

// Filter decides which files are eligible.
type Filter struct {
	AllFormats bool
}

func (f Filter) Accepts(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return lossless[ext] || (f.AllFormats && lossy[ext])
}

// Discover expands arguments — files, directories searched recursively, and
// globs — into absolute paths, deduplicated and sorted.
//
// Every argument must yield at least one accepted file, since an argument that
// yields none looks exactly like a mistyped one. Each such argument is
// reported on its own, so the rest are still converted.
func Discover(args []string, filter Filter) ([]string, []error) {
	found := newFinder(filter)
	var problems []error

	for _, arg := range args {
		matched, err := found.expand(arg)
		switch {
		case err != nil:
			problems = append(problems, err)
		case matched == 0:
			problems = append(problems, fmt.Errorf("%s: no matching audio files", arg))
		}
	}
	return found.sorted(), problems
}

// Select narrows paths that are already enumerated, which is what Lidarr mode
// does: the tracks arrive in the environment and patterns filter them. A
// pattern matching nothing is silence here, not a typo.
func Select(paths, patterns []string, filter Filter) ([]string, []error) {
	found := newFinder(filter)
	var problems []error

	for _, path := range paths {
		if len(patterns) > 0 && !matchAny(path, patterns) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if info.Mode().IsRegular() {
			found.admit(path)
		}
	}
	return found.sorted(), problems
}

// finder accumulates accepted files as absolute, deduplicated paths.
type finder struct {
	filter Filter
	seen   map[string]bool
	paths  []string
}

func newFinder(filter Filter) *finder {
	return &finder{filter: filter, seen: make(map[string]bool)}
}

// admit reports whether the filter accepts the file, having kept it if this is
// the first spelling of it: an argument overlapping an earlier one still counts
// as having matched.
func (f *finder) admit(path string) bool {
	if !f.filter.Accepts(path) {
		return false
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	if !f.seen[absolute] {
		f.seen[absolute] = true
		f.paths = append(f.paths, absolute)
	}
	return true
}

func (f *finder) sorted() []string {
	slices.Sort(f.paths)
	return f.paths
}

// expand resolves one argument and returns how many files it admitted.
func (f *finder) expand(arg string) (int, error) {
	if isPattern(arg) {
		matches, err := doublestar.FilepathGlob(arg, doublestar.WithFilesOnly())
		if err != nil {
			return 0, fmt.Errorf("%s: %w", arg, err)
		}
		return f.admitAll(matches), nil
	}

	info, err := os.Stat(arg)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		// A named file goes through the same checks as a discovered one, so
		// `-i cover.jpg` and a FIFO called track.flac are both reported.
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("%s: not a regular file", arg)
		}
		return f.admitAll([]string{arg}), nil
	}
	return f.walk(arg)
}

func (f *finder) admitAll(paths []string) int {
	accepted := 0
	for _, path := range paths {
		if f.admit(path) {
			accepted++
		}
	}
	return accepted
}

func (f *finder) walk(dir string) (int, error) {
	accepted := 0

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			// An unreadable subdirectory does not stop the rest of the walk.
			return nil
		case entry.IsDir():
			return nil
		case entry.Type().IsRegular(), pointsAtAFile(path):
			// Symlinks to files are followed, as they are when named or
			// globbed; sockets, devices and broken links are skipped.
			if f.admit(path) {
				accepted++
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("%s: %w", dir, err)
	}
	return accepted, nil
}

func pointsAtAFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isPattern(arg string) bool { return strings.ContainsAny(arg, "*?[") }

// matchAny matches a pattern against the base name, or against the whole path
// when the pattern itself contains a separator. That is what makes
// `-i '*.flac'` mean "the FLACs of this import" in Lidarr mode.
func matchAny(path string, patterns []string) bool {
	base := filepath.Base(path)

	for _, pattern := range patterns {
		target := base
		if strings.ContainsAny(pattern, `/`+string(filepath.Separator)) {
			target = path
		}
		if matched, err := doublestar.Match(filepath.ToSlash(pattern), filepath.ToSlash(target)); err == nil && matched {
			return true
		}
	}
	return false
}
