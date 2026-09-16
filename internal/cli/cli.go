// Package cli parses the command line.
//
// The syntax is FFmpeg-flavoured: short options take a following value
// (-f ogg), -i accepts any number of paths, and an unrecognised --key=value is
// forwarded to FFmpeg as -key value, in the order given, so the invocation
// stays reproducible.
package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"media-converter/internal/ffmpeg"
)

const (
	DefaultConcurrency = 4
	DefaultFormat      = "ogg"
)

// ErrNoInput reports that nothing to convert was named.
var ErrNoInput = errors.New("no input given; pass paths, -i PATH, or a glob such as -i '**/*.flac'")

// Request is what the command line said, before any of it is acted on.
type Request struct {
	Inputs        []string
	OutputDir     string
	FormatName    string
	CodecOverride string
	Options       []ffmpeg.Option
	Concurrency   int
	Remove        bool
	AllFormats    bool
	DryRun        bool
	FFmpegPath    string
	Help          bool
	Version       bool
}

type arity int

const (
	noValue arity = iota
	oneValue
	manyValues
)

// flag is one option this tool owns. The table below is where an option is
// declared: the parser dispatches through it and the help text is rendered
// from it.
type flag struct {
	short      string
	long       string
	arity      arity
	metavar    string
	help       string
	section    string
	repeatable bool
	set        func(*Request, string) error
}

var flags = []flag{{
	short: "-i", long: "--input", arity: manyValues, metavar: "PATH...", section: "INPUT",
	help: "files, directories or globs (quote them)", repeatable: true,
	set: func(r *Request, value string) error { r.Inputs = append(r.Inputs, value); return nil },
}, {
	long: "--all-formats", section: "INPUT",
	help: "accept lossy sources, not just lossless",
	set:  func(r *Request, _ string) error { r.AllFormats = true; return nil },
}, {
	short: "-o", long: "--output", arity: oneValue, metavar: "DIR", section: "OUTPUT",
	help: "destination directory",
	set:  func(r *Request, value string) error { r.OutputDir = value; return nil },
}, {
	short: "-f", long: "--format", arity: oneValue, metavar: "FORMAT", section: "OUTPUT",
	help: "output container (default " + DefaultFormat + ")",
	set:  func(r *Request, value string) error { r.FormatName = value; return nil },
}, {
	short: "-r", long: "--remove", section: "OUTPUT",
	help: "delete each source once it is converted",
	set:  func(r *Request, _ string) error { r.Remove = true; return nil },
}, {
	short: "-c", long: "--concurrency", arity: oneValue, metavar: "N", section: "OUTPUT",
	help: fmt.Sprintf("simultaneous conversions (default %d)", DefaultConcurrency),
	set: func(r *Request, value string) error {
		workers, err := strconv.Atoi(value)
		if err != nil || workers < 1 {
			return fmt.Errorf("%q is not a positive integer", value)
		}
		r.Concurrency = workers
		return nil
	},
}, {
	short: "-n", long: "--dry-run", section: "OUTPUT",
	help: "print the commands and convert nothing",
	set:  func(r *Request, _ string) error { r.DryRun = true; return nil },
}, {
	long: "--ffmpeg", arity: oneValue, metavar: "PATH", section: "FFMPEG",
	help: "executable to drive",
	set:  func(r *Request, value string) error { r.FFmpegPath = value; return nil },
}, {
	short: "-h", long: "--help", section: "OTHER",
	help: "show this help",
	set:  func(r *Request, _ string) error { r.Help = true; return nil },
}, {
	long: "--version", section: "OTHER",
	help: "show the version",
	set:  func(r *Request, _ string) error { r.Version = true; return nil },
}}

var known = func() map[string]*flag {
	index := make(map[string]*flag, 2*len(flags))
	for i := range flags {
		index[flags[i].long] = &flags[i]
		if flags[i].short != "" {
			index[flags[i].short] = &flags[i]
		}
	}
	return index
}()

// codecOptions select the audio encoder. They are taken out of the forwarded
// options so the encoder can be named in the plan and in --dry-run.
var codecOptions = map[string]bool{"acodec": true, "c:a": true, "codec:a": true}

// ffmpegOption is what an FFmpeg option name looks like: alphanumeric, then
// the punctuation its options use — "c:a", "compression_level", "x264-params".
var ffmpegOption = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:.-]*$`)

func Parse(args []string) (*Request, error) {
	p := parser{
		args:    args,
		request: Request{Concurrency: DefaultConcurrency, FormatName: DefaultFormat},
		seen:    make(map[string]bool),
	}
	if err := p.run(); err != nil {
		return nil, err
	}
	return &p.request, nil
}

type parser struct {
	args    []string
	at      int
	request Request
	seen    map[string]bool
	// pathsOnly is set by "--": everything after it is an input.
	pathsOnly bool
}

func (p *parser) run() error {
	for ; p.at < len(p.args); p.at++ {
		arg := p.args[p.at]

		switch {
		case p.pathsOnly, arg == "-", !strings.HasPrefix(arg, "-"):
			p.request.Inputs = append(p.request.Inputs, arg)
		case arg == "--":
			p.pathsOnly = true
		default:
			if err := p.option(arg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *parser) option(arg string) error {
	name, inline, inlined := cut(arg)

	switch {
	case known[name] != nil:
		return p.assign(known[name], name, inline, inlined)
	case strings.HasPrefix(name, "--"):
		return p.forward(arg, name, inline, inlined)
	default:
		return p.cluster(arg)
	}
}

func (p *parser) assign(f *flag, name, inline string, inlined bool) error {
	if !f.repeatable {
		if p.seen[f.long] {
			return fmt.Errorf("option %s given more than once", name)
		}
		p.seen[f.long] = true
	}

	switch f.arity {
	case noValue:
		if inlined {
			return fmt.Errorf("%s takes no value", name)
		}
		return f.set(&p.request, "")

	case oneValue:
		value, err := p.value(name, inline, inlined)
		if err != nil {
			return err
		}
		return annotate(name, f.set(&p.request, value))

	default:
		return p.values(f, name, inline, inlined)
	}
}

// values feeds a variadic option every following argument that is not itself a
// flag, so `-i '*.flac' '*.wav'` works without quoting tricks.
func (p *parser) values(f *flag, name, inline string, inlined bool) error {
	if inlined {
		value, err := p.value(name, inline, inlined)
		if err != nil {
			return err
		}
		return f.set(&p.request, value)
	}

	first := p.at + 1
	for p.at+1 < len(p.args) && !isFlag(p.args[p.at+1]) {
		p.at++
	}
	if p.at < first {
		return fmt.Errorf("%s requires at least one path", name)
	}
	for _, value := range p.args[first : p.at+1] {
		if err := f.set(&p.request, value); err != nil {
			return err
		}
	}
	return nil
}

// cluster handles a single-dash argument that is not an option by itself: a run
// of switches (-rn), or a switch carrying its value (-c8).
func (p *parser) cluster(arg string) error {
	letters := arg[1:]
	for i, letter := range letters {
		name := "-" + string(letter)
		f := known[name]
		if f == nil {
			return fmt.Errorf("unknown option %q", name)
		}
		if f.arity == noValue {
			if err := p.assign(f, name, "", false); err != nil {
				return err
			}
			continue
		}
		rest := letters[i+len(string(letter)):]
		return p.assign(f, name, rest, rest != "")
	}
	return nil
}

// forward records an unrecognised long option for FFmpeg. A value is
// mandatory, and an empty one passes a valueless flag: --shortest= forwards
// -shortest.
func (p *parser) forward(arg, name, inline string, inlined bool) error {
	if !inlined {
		return fmt.Errorf("unknown option %q (FFmpeg options must be given as --key=value)", arg)
	}
	key := strings.TrimPrefix(name, "--")
	if !ffmpegOption.MatchString(key) {
		return fmt.Errorf("malformed option %q: %q is not an FFmpeg option name", arg, key)
	}
	if p.seen[key] {
		return fmt.Errorf("option --%s given more than once", key)
	}
	p.seen[key] = true

	if codecOptions[key] {
		p.request.CodecOverride = inline
		return nil
	}
	p.request.Options = append(p.request.Options, ffmpeg.Option{Key: key, Value: inline})
	return nil
}

// value returns an inline --key=value payload, or consumes the next argument.
func (p *parser) value(name, inline string, inlined bool) (string, error) {
	if inlined {
		if inline == "" {
			return "", fmt.Errorf("%s requires a value", name)
		}
		return inline, nil
	}
	if p.at+1 >= len(p.args) {
		return "", fmt.Errorf("%s requires a value", name)
	}
	p.at++
	return p.args[p.at], nil
}

// cut splits --key=value at the first '=', so keys containing ':' survive.
func cut(arg string) (name, value string, inlined bool) {
	name, value, inlined = strings.Cut(arg, "=")
	return name, value, inlined
}

// isFlag reports whether arg ends a variadic list. A lone "-" is a path.
func isFlag(arg string) bool { return len(arg) > 1 && strings.HasPrefix(arg, "-") }

func annotate(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
