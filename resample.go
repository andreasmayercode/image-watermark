package watermark

import (
	"image"
	"image/draw"
	"math"
	"runtime"
	"sync"
)

// parallel runs fn(0..n-1) on all CPUs.
func parallel(n int, fn func(i int)) {
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	if workers <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	next := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// taps of a separable triangle filter (bilinear when enlarging,
// area-like anti-aliasing when reducing).
type taps struct {
	idx []int
	w   []float64
}

func makeTaps(src, dst int) []taps {
	scale := float64(src) / float64(dst)
	radius := math.Max(1, scale)
	out := make([]taps, dst)
	for i := range out {
		center := (float64(i)+0.5)*scale - 0.5
		lo := int(math.Ceil(center - radius))
		hi := int(math.Floor(center + radius))
		var t taps
		sum := 0.0
		for j := lo; j <= hi; j++ {
			w := 1 - math.Abs(float64(j)-center)/radius
			if w <= 0 {
				continue
			}
			k := min(max(j, 0), src-1)
			t.idx = append(t.idx, k)
			t.w = append(t.w, w)
			sum += w
		}
		for k := range t.w {
			t.w[k] /= sum
		}
		out[i] = t
	}
	return out
}

// workImage is the decoded picture in a form we can read luma from and
// write a luma delta into.
type workImage struct {
	img  image.Image // *image.YCbCr, *image.Gray or *image.NRGBA
	W, H int
	gate []uint8 // full-resolution texture weight 0..255 (nil = none)
}

func newWorkImage(src image.Image) *workImage {
	b := src.Bounds()
	switch src.(type) {
	case *image.YCbCr, *image.Gray, *image.NRGBA:
		return &workImage{img: src, W: b.Dx(), H: b.Dy()}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return &workImage{img: dst, W: b.Dx(), H: b.Dy()}
}

// lumaRow fills buf (len W) with the luma (0..255) of row y.
func (wi *workImage) lumaRow(y int, buf []float64) {
	b := wi.img.Bounds()
	switch p := wi.img.(type) {
	case *image.YCbCr:
		off := p.YOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < wi.W; x++ {
			buf[x] = float64(p.Y[off+x])
		}
	case *image.Gray:
		off := p.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < wi.W; x++ {
			buf[x] = float64(p.Pix[off+x])
		}
	case *image.NRGBA:
		off := p.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < wi.W; x++ {
			q := p.Pix[off+4*x:]
			buf[x] = 0.299*float64(q[0]) + 0.587*float64(q[1]) + 0.114*float64(q[2])
		}
	}
}

func clamp8(v float64) uint8 {
	v = math.Round(v)
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// addRow adds d (len W) to the luma of row y. For RGB images the same
// value goes into R, G and B, which changes Y and leaves Cb/Cr untouched.
func (wi *workImage) addRow(y int, d []float64) {
	b := wi.img.Bounds()
	switch p := wi.img.(type) {
	case *image.YCbCr:
		off := p.YOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < wi.W; x++ {
			p.Y[off+x] = clamp8(float64(p.Y[off+x]) + d[x])
		}
	case *image.Gray:
		off := p.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < wi.W; x++ {
			p.Pix[off+x] = clamp8(float64(p.Pix[off+x]) + d[x])
		}
	case *image.NRGBA:
		off := p.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < wi.W; x++ {
			q := p.Pix[off+4*x:]
			q[0] = clamp8(float64(q[0]) + d[x])
			q[1] = clamp8(float64(q[1]) + d[x])
			q[2] = clamp8(float64(q[2]) + d[x])
		}
	}
}

// canonical resamples the luma to the N×N working grid.
func (wi *workImage) canonical() []float64 { return wi.resampleLuma(N, N) }

// resampleLuma resamples the luma to ow×oh.
func (wi *workImage) resampleLuma(ow, oh int) []float64 {
	return wi.resampleRegion(0, 0, wi.W, wi.H, ow, oh)
}

// resampleRegion resamples the luma of the rectangle (x0, y0, rw, rh) to ow×oh.
func (wi *workImage) resampleRegion(x0, y0, rw, rh, ow, oh int) []float64 {
	tx := makeTaps(rw, ow)
	ty := makeTaps(rh, oh)
	hz := make([]float64, rh*ow)
	parallel(rh, func(y int) {
		full := make([]float64, wi.W)
		wi.lumaRow(y0+y, full)
		buf := full[x0 : x0+rw]
		row := hz[y*ow : y*ow+ow]
		for i, t := range tx {
			s := 0.0
			for k, j := range t.idx {
				s += t.w[k] * buf[j]
			}
			row[i] = s
		}
	})
	out := make([]float64, ow*oh)
	parallel(oh, func(i int) {
		row := out[i*ow : i*ow+ow]
		t := ty[i]
		for k, j := range t.idx {
			w := t.w[k]
			src := hz[j*ow : j*ow+ow]
			for x := range row {
				row[x] += w * src[x]
			}
		}
	})
	return out
}

// computeGate stores a per-pixel weight from the local luma standard
// deviation (5×5): flat pixels get ~0, texture and edges ~1. It is taken
// from the unmarked image once and reused by every embedding pass.
func (wi *workImage) computeGate() {
	const r, c = 2, 3.0
	wi.gate = make([]uint8, wi.W*wi.H)
	parallel(wi.H, func(y int) {
		rows := make([][]float64, 2*r+1)
		for k := range rows {
			rows[k] = make([]float64, wi.W)
			wi.lumaRow(min(max(y-r+k, 0), wi.H-1), rows[k])
		}
		col1 := make([]float64, wi.W)
		col2 := make([]float64, wi.W)
		for _, row := range rows {
			for x, v := range row {
				col1[x] += v
				col2[x] += v * v
			}
		}
		g := wi.gate[y*wi.W : y*wi.W+wi.W]
		for x := 0; x < wi.W; x++ {
			s1, s2, n := 0.0, 0.0, 0.0
			for j := max(x-r, 0); j <= min(x+r, wi.W-1); j++ {
				s1 += col1[j]
				s2 += col2[j]
				n += float64(2*r + 1)
			}
			sd := math.Sqrt(math.Max(s2/n-(s1/n)*(s1/n), 0))
			g[x] = uint8(math.Round(255 * sd / (sd + c)))
		}
	})
}

// applyDelta scales the N×N delta up (or down) to image size and adds it.
func (wi *workImage) applyDelta(delta []float64) {
	tx := makeTaps(N, wi.W)
	ty := makeTaps(N, wi.H)
	hz := make([]float64, N*wi.W)
	parallel(N, func(r int) {
		src := delta[r*N : r*N+N]
		row := hz[r*wi.W : r*wi.W+wi.W]
		for x, t := range tx {
			s := 0.0
			for k, j := range t.idx {
				s += t.w[k] * src[j]
			}
			row[x] = s
		}
	})
	parallel(wi.H, func(y int) {
		d := make([]float64, wi.W)
		t := ty[y]
		for k, j := range t.idx {
			w := t.w[k]
			src := hz[j*wi.W : j*wi.W+wi.W]
			for x := range d {
				d[x] += w * src[x]
			}
		}
		if wi.gate != nil {
			g := wi.gate[y*wi.W : y*wi.W+wi.W]
			for x := range d {
				d[x] *= float64(g[x]) / 255
			}
		}
		wi.addRow(y, d)
	})
}

// lumaPlane returns the luma of the whole image (rounded, 1 byte/pixel).
func (wi *workImage) lumaPlane() []uint8 {
	out := make([]uint8, wi.W*wi.H)
	parallel(wi.H, func(y int) {
		buf := make([]float64, wi.W)
		wi.lumaRow(y, buf)
		for x, v := range buf {
			out[y*wi.W+x] = clamp8(v)
		}
	})
	return out
}

// lumaRMSDiff compares the current luma with an earlier lumaPlane.
func (wi *workImage) lumaRMSDiff(before []uint8) float64 {
	rows := make([]float64, wi.H)
	parallel(wi.H, func(y int) {
		buf := make([]float64, wi.W)
		wi.lumaRow(y, buf)
		s := 0.0
		for x, v := range buf {
			d := math.Round(v) - float64(before[y*wi.W+x])
			s += d * d
		}
		rows[y] = s
	})
	e := 0.0
	for _, v := range rows {
		e += v
	}
	return math.Sqrt(e / float64(wi.W*wi.H))
}
