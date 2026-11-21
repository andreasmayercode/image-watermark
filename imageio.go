package watermark

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"

	_ "golang.org/x/image/webp"
)

const maxPixels = 120_000_000 // refuse decompression bombs

// Source is a decoded image with what is needed to write it back.
type Source struct {
	Image  image.Image
	Format string   // "jpeg", "png", "gif" or "webp"
	Meta   [][]byte // JPEG APP1 (EXIF/XMP), APP2 (ICC), APP13 (IPTC), COM segments
	cmyk   bool
}

// Decode reads a JPEG, PNG, GIF or WebP image (64×64 px up to 120 megapixels).
func Decode(data []byte) (*Source, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unsupported or broken image: %w", err)
	}
	if cfg.Width < 64 || cfg.Height < 64 {
		return nil, errors.New("image too small (minimum 64×64 px)")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("image too large (%d×%d px)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding failed: %w", err)
	}
	l := &Source{Image: img, Format: format}
	if format == "jpeg" {
		_, l.cmyk = img.(*image.CMYK)
		l.Meta = jpegMetaSegments(data, l.cmyk)
	}
	return l, nil
}

// Load reads an image file ("-" = stdin), see Decode.
func Load(path string) (*Source, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// jpegMetaSegments collects the metadata segments up to the first SOS so
// they can be written into the new file (orientation, color profile, ...).
func jpegMetaSegments(data []byte, cmyk bool) [][]byte {
	var segs [][]byte
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			break
		}
		marker := data[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // SOS / EOI
			break
		}
		n := int(data[i+2])<<8 | int(data[i+3])
		if n < 2 || i+2+n > len(data) {
			break
		}
		seg := data[i : i+2+n]
		switch {
		case marker == 0xE1, marker == 0xED, marker == 0xFE:
			segs = append(segs, seg)
		case marker == 0xE2 && !cmyk: // a CMYK profile would not fit the RGB output
			segs = append(segs, seg)
		}
		i += 2 + n
	}
	return segs
}

// OutputFormat picks "jpeg" or "png" by the extension of path; without one
// it keeps fallback (if writable) or uses PNG.
func OutputFormat(path, fallback string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "jpeg", nil
	case ".png":
		return "png", nil
	case "":
		if fallback == "jpeg" || fallback == "png" {
			return fallback, nil
		}
		return "png", nil
	}
	return "", fmt.Errorf("output format %q not supported (use .jpg or .png)", filepath.Ext(path))
}

// Encode writes img as JPEG (with the metadata segments meta) or PNG.
func Encode(w io.Writer, img image.Image, format string, quality int, meta [][]byte) error {
	if format == "png" {
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		return enc.Encode(w, img)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	out := buf.Bytes()
	if len(meta) > 0 {
		if _, err := w.Write(out[:2]); err != nil { // SOI
			return err
		}
		for _, s := range meta {
			if _, err := w.Write(s); err != nil {
				return err
			}
		}
		out = out[2:]
	}
	_, err := w.Write(out)
	return err
}

// Save writes img atomically (temporary file + rename; "-" = stdout).
func Save(path string, img image.Image, format string, quality int, meta [][]byte) error {
	if path == "-" {
		return Encode(os.Stdout, img, format, quality, meta)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wm-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := Encode(tmp, img, format, quality, meta); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	os.Chmod(tmp.Name(), mode)
	return os.Rename(tmp.Name(), path)
}
