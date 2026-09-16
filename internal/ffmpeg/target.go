package ffmpeg

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Option is one FFmpeg option, emitted as "-Key Value", or as the bare flag
// "-Key" when Value is empty.
type Option struct {
	Key   string
	Value string
}

// Format is an output container: the muxer to write, the extension to give the
// file, the encoder to run, and the options to run it with.
type Format struct {
	Muxer    string
	Ext      string
	Codec    string
	CoverArt bool
	Lossless bool
	Defaults []Option
}

// Rate control at or above each codec's transparency threshold, since FFmpeg's
// own defaults aim at streaming: libvorbis lands near 112 kbps, libmp3lame at
// 128 kbps CBR, libopus and the native AAC encoder at 128 kbps.
//
// MP3 uses LAME's best VBR target with its slowest analysis effort rather than
// 320 CBR: -q:a selects a quality target, not a bitrate, and V0 averages
// ~245 kbps at the same perceived quality.
var (
	vorbis = []Option{{Key: "q:a", Value: "7"}}
	mp3    = []Option{{Key: "q:a", Value: "0"}, {Key: "compression_level", Value: "0"}}
	opus   = []Option{{Key: "b:a", Value: "192k"}}
	aac    = []Option{{Key: "b:a", Value: "256k"}}
	ac3    = []Option{{Key: "b:a", Value: "640k"}}
)

// PCM targets encode at 24 bits: 16-bit sources widen, where a 24-bit source
// written as pcm_s16le is truncated irrecoverably.
var formats = map[string]Format{
	"aac":  {Muxer: "ipod", Ext: ".m4a", Codec: "aac", CoverArt: true, Defaults: aac},
	"ac3":  {Muxer: "ac3", Ext: ".ac3", Codec: "ac3", Defaults: ac3},
	"aiff": {Muxer: "aiff", Ext: ".aiff", Codec: "pcm_s24be", Lossless: true},
	"alac": {Muxer: "ipod", Ext: ".m4a", Codec: "alac", CoverArt: true, Lossless: true},
	"flac": {Muxer: "flac", Ext: ".flac", Codec: "flac", CoverArt: true, Lossless: true},
	"mka":  {Muxer: "matroska", Ext: ".mka", Codec: "libvorbis", CoverArt: true, Defaults: vorbis},
	"mp3":  {Muxer: "mp3", Ext: ".mp3", Codec: "libmp3lame", CoverArt: true, Defaults: mp3},
	"oga":  {Muxer: "ogg", Ext: ".oga", Codec: "libvorbis", Defaults: vorbis},
	"ogg":  {Muxer: "ogg", Ext: ".ogg", Codec: "libvorbis", Defaults: vorbis},
	"opus": {Muxer: "opus", Ext: ".opus", Codec: "libopus", Defaults: opus},
	"wav":  {Muxer: "wav", Ext: ".wav", Codec: "pcm_s24le", Lossless: true},
	"wv":   {Muxer: "wv", Ext: ".wv", Codec: "wavpack", Lossless: true},
}

var aliases = map[string]string{
	"aif":      "aiff",
	"ipod":     "aac",
	"lame":     "mp3",
	"m4a":      "aac",
	"m4b":      "aac",
	"matroska": "mka",
	"mp4":      "aac",
	"vorbis":   "ogg",
	"wave":     "wav",
	"wavpack":  "wv",
}

// Names lists the values -f accepts, aliases aside.
func Names() []string { return slices.Sorted(maps.Keys(formats)) }

// Target is a resolved output: the container, the encoder that will run, and
// the options it will run with.
type Target struct {
	Format  Format
	Codec   string
	Options []Option
}

// Resolve turns a format name, an optional encoder override and the user's
// forwarded options into the target a run uses.
//
// A format's defaults suit its own encoder, so an override drops them whole —
// -q:a means nothing to libopus — while the same encoder merges them beneath
// the user's options.
func Resolve(name, codec string, user []Option) (Target, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := aliases[key]; ok {
		key = canonical
	}
	format, ok := formats[key]
	if !ok {
		return Target{}, fmt.Errorf("unsupported output format %q (supported: %s)", name, strings.Join(Names(), ", "))
	}

	if codec != "" && codec != format.Codec {
		return Target{Format: format, Codec: codec, Options: user}, nil
	}
	return Target{Format: format, Codec: format.Codec, Options: merge(format.Defaults, user)}, nil
}

// rateControl options select an encoder's quality or bitrate. They are
// mutually exclusive in practice, so a user who sets one has decided rate
// control entirely.
var rateControl = map[string]bool{
	"q:a": true, "qscale:a": true, "qscale": true, "aq": true,
	"b:a": true, "ab": true, "global_quality": true, "vbr": true,
}

func merge(defaults, user []Option) []Option {
	userKeys := make(map[string]bool, len(user))
	userSetsRate := false
	for _, opt := range user {
		userKeys[opt.Key] = true
		userSetsRate = userSetsRate || rateControl[opt.Key]
	}

	merged := make([]Option, 0, len(defaults)+len(user))
	for _, opt := range defaults {
		if userKeys[opt.Key] || (userSetsRate && rateControl[opt.Key]) {
			continue
		}
		merged = append(merged, opt)
	}
	return append(merged, user...)
}

// Args is the command line converting input into output.
//
// Stream selection is explicit: left to itself FFmpeg picks the best audio
// stream and the best video stream, and a cover-art picture given to a muxer
// that cannot carry one aborts the conversion.
func (t Target) Args(input, output string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", input, "-map", "0:a"}
	if t.Format.CoverArt {
		args = append(args, "-map", "0:v?", "-c:v", "copy")
	}
	args = append(args, "-map_metadata", "0", "-c:a", t.Codec)

	for _, opt := range t.Options {
		args = append(args, "-"+opt.Key)
		if opt.Value != "" {
			args = append(args, opt.Value)
		}
	}
	return append(args, "-f", t.Format.Muxer, output)
}

// Quote renders a command as a shell would accept it, for --dry-run. Tokens of
// safe characters are left bare, so only the paths carry quotes.
func Quote(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quote(arg)
	}
	return strings.Join(quoted, " ")
}

func quote(arg string) string {
	if strings.IndexFunc(arg, unsafeInShell) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func unsafeInShell(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	default:
		return !strings.ContainsRune("-_./:=+,@", r)
	}
}
