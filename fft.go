package watermark

import (
	"math"
	"math/bits"
	"math/cmplx"
	"sync"
)

// Radix-2 complex FFT (in place, unnormalized). Sizes must be powers of two.

var (
	twMu    sync.Mutex
	twCache = map[int][]complex128{}
)

func twiddles(n int) []complex128 {
	twMu.Lock()
	defer twMu.Unlock()
	if t, ok := twCache[n]; ok {
		return t
	}
	t := make([]complex128, n/2)
	for i := range t {
		t[i] = cmplx.Rect(1, -2*math.Pi*float64(i)/float64(n))
	}
	twCache[n] = t
	return t
}

// fft1 transforms a in place; inverse uses e^{+i} and no 1/n factor.
func fft1(a []complex128, inverse bool) {
	n := len(a)
	if n <= 1 {
		return
	}
	shift := 64 - uint(bits.TrailingZeros(uint(n)))
	for i := 0; i < n; i++ {
		j := int(bits.Reverse64(uint64(i)) >> shift)
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}
	tw := twiddles(n)
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				w := tw[k*step]
				if inverse {
					w = cmplx.Conj(w)
				}
				u, v := a[start+k], a[start+k+half]*w
				a[start+k], a[start+k+half] = u+v, u-v
			}
		}
	}
}

// fft2 transforms an h×w array (row-major) in place.
func fft2(a []complex128, w, h int, inverse bool) {
	parallel(h, func(y int) { fft1(a[y*w:y*w+w], inverse) })
	parallel(w, func(x int) {
		col := make([]complex128, h)
		for y := 0; y < h; y++ {
			col[y] = a[y*w+x]
		}
		fft1(col, inverse)
		for y := 0; y < h; y++ {
			a[y*w+x] = col[y]
		}
	})
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
