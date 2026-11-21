package watermark

// Robustness measurement on real photos (skipped without WM_IMAGES):
//
//	WM_IMAGES=/imgs WM_LIMIT=10 go test -run TestAttacks -v -timeout 60m
//
// Every image is marked, attacked and read back; the table lists how many
// were read correctly. WM_ONLY=crop,rot limits the attacks by name prefix.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func jpegTrip(img image.Image, q int) image.Image {
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
	out, _ := jpeg.Decode(&buf)
	return out
}

func cropImg(img image.Image, l, t, r, b float64) image.Image {
	n := toNRGBA(img)
	W, H := n.Bounds().Dx(), n.Bounds().Dy()
	rect := image.Rect(int(float64(W)*l), int(float64(H)*t), W-int(float64(W)*r), H-int(float64(H)*b))
	dst := image.NewNRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), n, rect.Min, draw.Src)
	return dst
}

func resizeImg(img image.Image, w, h int) *image.NRGBA {
	n := toNRGBA(img)
	W, H := n.Bounds().Dx(), n.Bounds().Dy()
	tx, ty := makeTaps(W, w), makeTaps(H, h)
	hz := make([]float64, H*w*3)
	for y := 0; y < H; y++ {
		for x, t := range tx {
			for c := 0; c < 3; c++ {
				s := 0.0
				for k, j := range t.idx {
					s += t.w[k] * float64(n.Pix[y*n.Stride+4*j+c])
				}
				hz[(y*w+x)*3+c] = s
			}
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y, t := range ty {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				s := 0.0
				for k, j := range t.idx {
					s += t.w[k] * hz[(j*w+x)*3+c]
				}
				dst.Pix[y*dst.Stride+4*x+c] = clamp8(s)
			}
			dst.Pix[y*dst.Stride+4*x+3] = 255
		}
	}
	return dst
}

// rotateImg rotates by deg (clockwise) around the center onto an expanded
// canvas (fill gray); inner=true crops to the largest axis-aligned rect
// (approximately) instead.
func rotateImg(img image.Image, deg float64, inner bool) image.Image {
	n := toNRGBA(img)
	W, H := float64(n.Bounds().Dx()), float64(n.Bounds().Dy())
	th := deg * math.Pi / 180
	c, s := math.Cos(th), math.Sin(th)
	nw := math.Abs(W*c) + math.Abs(H*s)
	nh := math.Abs(W*s) + math.Abs(H*c)
	dst := image.NewNRGBA(image.Rect(0, 0, int(nw), int(nh)))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{color.NRGBA{128, 128, 128, 255}}, image.Point{}, draw.Src)
	cx, cy := W/2, H/2
	dx, dy := nw/2, nh/2
	for y := 0; y < int(nh); y++ {
		for x := 0; x < int(nw); x++ {
			px, py := float64(x)+0.5-dx, float64(y)+0.5-dy
			sx := c*px + s*py + cx - 0.5
			sy := -s*px + c*py + cy - 0.5
			if sx < 0 || sy < 0 || sx > W-1 || sy > H-1 {
				continue
			}
			x0, y0 := int(sx), int(sy)
			x1, y1 := min(x0+1, int(W)-1), min(y0+1, int(H)-1)
			rx, ry := sx-float64(x0), sy-float64(y0)
			for ch := 0; ch < 3; ch++ {
				g := func(xx, yy int) float64 { return float64(n.Pix[yy*n.Stride+4*xx+ch]) }
				v := (1-rx)*(1-ry)*g(x0, y0) + rx*(1-ry)*g(x1, y0) + (1-rx)*ry*g(x0, y1) + rx*ry*g(x1, y1)
				dst.Pix[y*dst.Stride+4*x+ch] = clamp8(v)
			}
		}
	}
	if !inner {
		return dst
	}
	// crop the inner rectangle of the same aspect ratio
	a := math.Abs(th)
	k := 1 / (math.Cos(a) + math.Max(W/H, H/W)*math.Sin(a))
	iw, ih := W*k, H*k
	return cropImg(dst, (nw-iw)/2/nw, (nh-ih)/2/nh, (nw-iw)/2/nw, (nh-ih)/2/nh)
}

// collage pastes img scaled to frac of the canvas width at (px, py) into bg.
func collage(img, bg image.Image, cw, ch int, frac, px, py float64) image.Image {
	canvas := resizeImg(bg, cw, ch)
	b := img.Bounds()
	w := int(float64(cw) * frac)
	h := w * b.Dy() / b.Dx()
	small := resizeImg(img, w, h)
	x0, y0 := int(px*float64(cw-w)), int(py*float64(ch-h))
	draw.Draw(canvas, image.Rect(x0, y0, x0+w, y0+h), small, image.Point{}, draw.Src)
	return canvas
}

type attack struct {
	name string
	fn   func(img, other image.Image) image.Image
}

func attacks() []attack {
	scaleW := func(w int) func(img, _ image.Image) image.Image {
		return func(img, _ image.Image) image.Image {
			b := img.Bounds()
			return jpegTrip(resizeImg(img, w, max(w*b.Dy()/b.Dx(), 1)), 80)
		}
	}
	return []attack{
		{"none jpeg85", func(img, _ image.Image) image.Image { return jpegTrip(img, 85) }},
		{"jpeg50", func(img, _ image.Image) image.Image { return jpegTrip(img, 50) }},
		{"scale 25%", func(img, _ image.Image) image.Image {
			b := img.Bounds()
			return jpegTrip(resizeImg(img, b.Dx()/4, b.Dy()/4), 85)
		}},
		{"scale 400px", scaleW(400)},
		{"scale 300px", scaleW(300)},
		{"aniso 70x50", func(img, _ image.Image) image.Image {
			b := img.Bounds()
			return jpegTrip(resizeImg(img, b.Dx()*7/10, b.Dy()/2), 85)
		}},
		{"rot90", func(img, _ image.Image) image.Image { return jpegTrip(rotateImg(img, 90, false), 85) }},
		{"crop 2%", func(img, _ image.Image) image.Image { return jpegTrip(cropImg(img, 0.02, 0.02, 0.02, 0.02), 85) }},
		{"crop 10/3/17/8%", func(img, _ image.Image) image.Image { return jpegTrip(cropImg(img, 0.10, 0.03, 0.17, 0.08), 85) }},
		{"crop center 50%", func(img, _ image.Image) image.Image { return jpegTrip(cropImg(img, 0.25, 0.25, 0.25, 0.25), 85) }},
		{"crop 30% + scale 50%", func(img, _ image.Image) image.Image {
			c := cropImg(img, 0.13, 0.21, 0.17, 0.09)
			b := c.Bounds()
			return jpegTrip(resizeImg(c, b.Dx()/2, b.Dy()/2), 80)
		}},
		{"rot 3°", func(img, _ image.Image) image.Image { return jpegTrip(rotateImg(img, 3, false), 85) }},
		{"rot 17° inner", func(img, _ image.Image) image.Image { return jpegTrip(rotateImg(img, 17, true), 85) }},
		{"rot -33°", func(img, _ image.Image) image.Image { return jpegTrip(rotateImg(img, -33, false), 85) }},
		{"rot 45° + 50%", func(img, _ image.Image) image.Image {
			r := rotateImg(img, 45, false)
			b := r.Bounds()
			return jpegTrip(resizeImg(r, b.Dx()/2, b.Dy()/2), 85)
		}},
		{"collage 60%", func(img, o image.Image) image.Image { return jpegTrip(collage(img, o, 1600, 1200, 0.6, 0.3, 0.7), 85) }},
		{"collage 40%", func(img, o image.Image) image.Image { return jpegTrip(collage(img, o, 1600, 1200, 0.4, 0.8, 0.2), 85) }},
		{"collage 25%", func(img, o image.Image) image.Image { return jpegTrip(collage(img, o, 1600, 1200, 0.25, 0.1, 0.5), 85) }},
		{"blur", func(img, _ image.Image) image.Image {
			b := img.Bounds()
			s := resizeImg(img, b.Dx()/3, b.Dy()/3)
			return jpegTrip(resizeImg(s, b.Dx(), b.Dy()), 85)
		}},
	}
}

func TestAttacks(t *testing.T) {
	dir := os.Getenv("WM_IMAGES")
	if dir == "" {
		t.Skip("WM_IMAGES not set")
	}
	files := testImages(dir)
	if n, _ := strconv.Atoi(os.Getenv("WM_LIMIT")); n > 0 && n < len(files) {
		files = files[:n]
	}
	only := os.Getenv("WM_ONLY")
	strength := 1.0
	if v, err := strconv.ParseFloat(os.Getenv("WM_STRENGTH"), 64); err == nil {
		strength = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("WM_SYNC_AMP"), 64); err == nil {
		syncAmp = v
	}
	ls := newLayouts([]byte("test-key-0123456789"), DefaultNamespace)
	atts := attacks()
	ok := make([]int, len(atts))
	conf := make([]float64, len(atts))
	var psnr, embedT, detT float64
	verified := 0
	const id = 3141592653
	for fi, f := range files {
		src, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		other, _ := Load(files[(fi+1)%len(files)])
		wi := newWorkImage(src.Image)
		t0 := time.Now()
		info := embedAll(wi, ls, id, strength, true)
		embedT += time.Since(t0).Seconds()
		psnr += info.PSNR
		if info.Verified {
			verified++
		}
		marked := wi.img
		var line []string
		for ai, a := range atts {
			if only != "" && !hasPrefixAny(a.name, only) {
				continue
			}
			img := a.fn(marked, other.Image)
			t1 := time.Now()
			r := detectAny(newWorkImage(img), ls, false)
			detT += time.Since(t1).Seconds()
			if r.Found && *r.ID == id {
				ok[ai]++
				line = append(line, "+")
			} else if r.Synced {
				line = append(line, "s")
			} else {
				line = append(line, "-")
				if r.Found {
					t.Errorf("%s %s: WRONG id %d", filepath.Base(f), a.name, *r.ID)
				}
			}
			conf[ai] += r.Confidence
		}
		t.Logf("%-36s psnr %.1f rms %.2f verified %v conf %.1f sync %.1f tex %.2f  %s", filepath.Base(f), info.PSNR,
			info.DeltaRMS, info.Verified, info.Confidence, info.SyncConfidence, info.Texture, strings.Join(line, ""))
	}
	n := float64(len(files))
	t.Logf("images %d, verified %d, mean PSNR %.1f, embed %.2fs/img, detect %.2fs/check", len(files), verified, psnr/n,
		embedT/n, detT/n/float64(len(atts)))
	for ai, a := range atts {
		if only != "" && !hasPrefixAny(a.name, only) {
			continue
		}
		t.Logf("%-22s %2d/%d  conf %.1f", a.name, ok[ai], len(files), conf[ai]/n)
	}
}

// testImages lists the JPEG and PNG files of dir; WM_GLOB narrows them (e.g. "*.png").
func testImages(dir string) []string {
	pats := []string{"*.jpg", "*.JPG", "*.jpeg", "*.png", "*.PNG"}
	if g := os.Getenv("WM_GLOB"); g != "" {
		pats = strings.Split(g, ",")
	}
	var files []string
	for _, p := range pats {
		m, _ := filepath.Glob(filepath.Join(dir, p))
		files = append(files, m...)
	}
	sort.Strings(files)
	return files
}

func hasPrefixAny(s, list string) bool {
	for _, p := range strings.Split(list, ",") {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func TestFalsePositives(t *testing.T) {
	dir := os.Getenv("WM_IMAGES")
	if dir == "" {
		t.Skip("WM_IMAGES not set")
	}
	files := testImages(dir)
	ls := newLayouts([]byte("other-key-987654321"), DefaultNamespace)
	maxConf := 0.0
	for _, f := range files {
		src, _ := Load(f)
		for _, img := range []image.Image{src.Image, cropImg(src.Image, 0.1, 0.1, 0, 0), rotateImg(src.Image, 10, false)} {
			r := detectAny(newWorkImage(img), ls, false)
			if r.Found {
				t.Errorf("%s: false positive %d", f, *r.ID)
			}
			maxConf = math.Max(maxConf, r.Confidence)
		}
	}
	fmt.Printf("checked %d×3, highest confidence %.2f\n", len(files), maxConf)
}
