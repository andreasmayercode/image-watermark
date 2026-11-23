// Package cli is the command line program of image-watermark (embed, detect,
// serve, version). Applications build their own program on it: their name,
// key variable, namespace, checking page and an optional Codec that shows the
// 32-bit id as text (e.g. a short code).
//
// Copyright (C) 2026 the image-watermark authors (https://github.com/andreasmayercode/image-watermark)
// SPDX-License-Identifier: AGPL-3.0-or-later
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	watermark "github.com/andreasmayercode/image-watermark"
)

// Codec shows the id as text: embed gets a flag -{Name}, detect and embed put
// a field "{Name}" into their JSON output if the id is a valid text.
type Codec interface {
	Name() string                       // flag and JSON field, e.g. "code"
	Usage() string                      // one line for the help text of the flag
	Encode(text string) (uint32, error) // text -> id; the error is shown to the user
	Decode(id uint32) (string, bool)    // id -> text; false = the id is no valid text
}

// Config describes a program.
type Config struct {
	Name      string // program name, e.g. "image-watermark"
	Version   string
	KeyEnv    string // environment variable with the key, e.g. "IMAGE_WM_KEY"
	Namespace string // watermark.Options.Namespace ("" = default)
	Codec     Codec  // optional
	IndexHTML []byte // checking page for serve (nil = the one of package web)
}

// Main runs the program with os.Args and exits.
func Main(cfg Config) {
	os.Exit(Run(cfg, os.Args[1:]))
}

// Run runs one command and returns the exit code (0 = ok / found,
// 1 = detect found nothing, 2 = error).
func Run(cfg Config, args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, cfg.usage())
		return 2
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(40) // big images: keep the heap close to what is in use
	}
	cmd, args := args[0], args[1:]
	var err error
	code := 0
	switch cmd {
	case "embed":
		err = cfg.embed(args)
	case "detect":
		code, err = cfg.detect(args)
	case "serve":
		err = cfg.serve(args)
	case "version", "-version", "--version":
		fmt.Println(cfg.Version)
	case "help", "-h", "-help", "--help":
		fmt.Print(cfg.usage())
	default:
		fmt.Fprint(os.Stderr, cfg.usage())
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	return code
}

func (cfg Config) usage() string {
	n := cfg.Name
	idLine := "  -id N                 number to hide, 0 … 4294967295\n"
	embedArgs := "-id ID"
	if cfg.Codec != nil {
		c := cfg.Codec.Name()
		idLine = fmt.Sprintf("  %-21s %s\n  -id N                 or a number to hide, 0 … 4294967295\n", "-"+c+" TEXT", cfg.Codec.Usage())
		embedArgs = fmt.Sprintf("-%s %s|-id ID", c, strings.ToUpper(c))
	}
	return fmt.Sprintf(`%[1]s %[2]s – invisible, robust image watermark

Usage:
  %[1]s embed  -in IN -out OUT %[3]s [options]
  %[1]s detect -in IN [-json] [options]
  %[1]s serve  [-listen 127.0.0.1:8080] [options]
  %[1]s version

Key (required, the same for embed and detect), first match wins:
  -key-file FILE        file with the secret key (recommended)
  %-21[4]s environment variable
  -key KEY              on the command line (visible in the process list)

Namespace (optional, the same for embed and detect):
  -namespace NS         separates the marks of different applications
                        (letters, digits, ".", "_", "-"; default %[6]q)

embed:
%[5]s  -strength F           1.0 = default; higher = more robust, more visible (0.3 … 3)
  -quality Q            JPEG quality of the output (default 92)
  -no-mask              spread the watermark evenly (no perceptual mask)
  -json                 print the result as JSON
  IN/OUT may be "-" for stdin/stdout. Input: JPEG, PNG, GIF, WebP;
  output: JPEG or PNG (by extension). JPEG metadata (EXIF, ICC, XMP) is kept.

detect:
  -json                 JSON output; -v adds the score of every bit
  Reads the id directly; if that fails, it looks for the sync pattern
  (cropped, rotated or pasted images) – "synced": true in the result.
  Exit code: 0 = watermark found, 1 = none, 2 = error.

serve:
  -listen ADDR          HTTP address (default 127.0.0.1:8080)
  -max-mb N             upload limit in MB (default 30)
`, n, cfg.Version, embedArgs, cfg.KeyEnv, idLine, cfg.namespace())
}

// namespace is the default of the flag -namespace: the one of the program,
// otherwise watermark.DefaultNamespace.
func (cfg Config) namespace() string {
	if cfg.Namespace != "" {
		return cfg.Namespace
	}
	return watermark.DefaultNamespace
}

// keyFlags are the flags every command needs: key and namespace.
type keyFlags struct{ file, inline, namespace *string }

func (cfg Config) addKeyFlags(fs *flag.FlagSet) keyFlags {
	return keyFlags{
		file:      fs.String("key-file", "", "file with the secret key"),
		inline:    fs.String("key", "", "secret key (visible in the process list)"),
		namespace: fs.String("namespace", cfg.namespace(), "namespace of the watermark"),
	}
}

// watermarker loads the key (file, environment, flag) and prepares the layouts
// for the namespace (flag -namespace, default the one of the program).
func (cfg Config) watermarker(k keyFlags) (*watermark.Watermarker, error) {
	var key string
	switch {
	case *k.file != "":
		b, err := os.ReadFile(*k.file)
		if err != nil {
			return nil, err
		}
		key = strings.TrimSpace(string(b))
	case cfg.KeyEnv != "" && os.Getenv(cfg.KeyEnv) != "":
		key = os.Getenv(cfg.KeyEnv)
	default:
		key = *k.inline
	}
	if len(key) < watermark.MinKeyLength {
		return nil, fmt.Errorf("secret key missing or shorter than %d characters (-key-file, %s or -key)", watermark.MinKeyLength, cfg.KeyEnv)
	}
	return watermark.New([]byte(key), watermark.Options{Namespace: *k.namespace})
}

// label shows the id with its text (Codec), if it is one.
func (cfg Config) label(id uint32) string {
	if cfg.Codec != nil {
		if t, ok := cfg.Codec.Decode(id); ok {
			return fmt.Sprintf("%s %q, id %d", cfg.Codec.Name(), t, id)
		}
	}
	return fmt.Sprintf("id %d", id)
}

// withText turns a result into a JSON object with the text of the id (Codec)
// next to "id". (The library rounds every measurement with round2, so no NaN
// or ±Inf - which JSON cannot carry - reaches this point.)
func (cfg Config) withText(v any, id *uint32) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	json.Unmarshal(b, &m)
	if cfg.Codec != nil && id != nil {
		if t, ok := cfg.Codec.Decode(*id); ok {
			m[cfg.Codec.Name()] = t
		}
	}
	return m
}

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func (cfg Config) embed(args []string) error {
	fs := flag.NewFlagSet("embed", flag.ExitOnError)
	in := fs.String("in", "", "input image")
	out := fs.String("out", "", "output image")
	idStr := fs.String("id", "", "number to hide")
	var textStr *string
	if cfg.Codec != nil {
		textStr = fs.String(cfg.Codec.Name(), "", cfg.Codec.Usage())
	} else {
		textStr = new(string)
	}
	strength := fs.Float64("strength", 1.0, "strength")
	quality := fs.Int("quality", 92, "JPEG quality")
	noMask := fs.Bool("no-mask", false, "no perceptual mask")
	asJSON := fs.Bool("json", false, "JSON output")
	kf := cfg.addKeyFlags(fs)
	fs.Parse(args)
	if *in == "" || *out == "" || (*idStr == "") == (*textStr == "") {
		if cfg.Codec != nil {
			return fmt.Errorf("embed needs -in, -out and either -%s or -id", cfg.Codec.Name())
		}
		return errors.New("embed needs -in, -out and -id")
	}
	var id uint32
	if *textStr != "" {
		v, err := cfg.Codec.Encode(*textStr)
		if err != nil {
			return err
		}
		id = v
	} else {
		v, err := strconv.ParseUint(*idStr, 10, 32)
		if err != nil {
			return errors.New("-id must be a number between 0 and 4294967295")
		}
		id = uint32(v)
	}
	if *strength < 0.1 || *strength > 5 {
		return errors.New("-strength must be between 0.1 and 5")
	}
	if *quality < 50 || *quality > 100 {
		return errors.New("-quality must be between 50 and 100")
	}
	w, err := cfg.watermarker(kf)
	if err != nil {
		return err
	}
	src, err := watermark.Load(*in)
	if err != nil {
		return err
	}
	format, err := watermark.OutputFormat(*out, src.Format)
	if err != nil {
		return err
	}
	marked, info := w.Embed(src.Image, id, watermark.EmbedOptions{Strength: *strength, NoMask: *noMask})
	meta := src.Meta
	if src.Format != "jpeg" || format != "jpeg" {
		meta = nil
	}
	if err := watermark.Save(*out, marked, format, *quality, meta); err != nil {
		return err
	}
	if !info.Verified {
		fmt.Fprintf(os.Stderr, "warning: watermark too weak to be read back reliably (confidence %.1f) – "+
			"the image has too little texture; try -strength 2 or -no-mask\n", finite(info.Confidence))
	}
	if info.Verified && info.SyncConfidence < watermark.SyncThreshold {
		fmt.Fprintf(os.Stderr, "warning: sync pattern weak (%.1f) – the id may not be readable after cropping or rotation\n", finite(info.SyncConfidence))
	}
	if info.Verified && info.Texture < watermark.LowTexture {
		fmt.Fprintf(os.Stderr, "warning: the image has large flat areas (texture %.2f, e.g. a graphic or screenshot) – "+
			"the watermark may be visible there\n", finite(info.Texture))
	}
	if *out == "-" {
		return nil
	}
	b := marked.Bounds()
	if *asJSON {
		m := cfg.withText(info, &id)
		m["ok"], m["id"], m["width"], m["height"] = true, id, b.Dx(), b.Dy()
		delete(m, "passes")
		return json.NewEncoder(os.Stdout).Encode(m)
	}
	fmt.Printf("watermark (%s) embedded: %s (%d×%d, verified %v, confidence %.1f, luma change RMS %.2f, PSNR %.1f dB)\n",
		cfg.label(id), *out, b.Dx(), b.Dy(), info.Verified, finite(info.Confidence), finite(info.DeltaRMS), finite(info.PSNR))
	return nil
}

func (cfg Config) detect(args []string) (int, error) {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	in := fs.String("in", "", "image to check")
	asJSON := fs.Bool("json", false, "JSON output")
	verbose := fs.Bool("v", false, "per-bit scores")
	kf := cfg.addKeyFlags(fs)
	fs.Parse(args)
	if *in == "" {
		return 2, errors.New("detect needs -in")
	}
	w, err := cfg.watermarker(kf)
	if err != nil {
		return 2, err
	}
	src, err := watermark.Load(*in)
	if err != nil {
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "error", "error": err.Error()})
		}
		return 2, err
	}
	r := w.Detect(src.Image, *verbose)
	code := 1
	if r.Found {
		code = 0
	}
	if *asJSON {
		return code, json.NewEncoder(os.Stdout).Encode(cfg.withText(r, r.ID))
	}
	switch r.Status {
	case "found":
		fmt.Printf("watermark found: %s (confidence %.1f)\n", cfg.label(*r.ID), r.Confidence)
	case "present_unreadable":
		fmt.Printf("watermark present, but the id is unreadable (confidence %.1f)\n", r.Confidence)
	default:
		fmt.Printf("no watermark found (confidence %.1f)\n", r.Confidence)
	}
	if r.Found && r.Orientation != "none" {
		fmt.Printf("image was %s\n", r.Orientation)
	}
	if *verbose {
		fmt.Printf("min bit z %.2f, crc ok %v, corrected %d, bits %v\n", r.MinBitZ, r.CRCOK, r.Corrected, r.BitZ)
	}
	return code, nil
}
