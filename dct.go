package watermark

import (
	"math"
	"sync"
)

// Orthonormal DCT-II on an N×N grid, computed only for the low-frequency
// K×K corner the watermark lives in (u, v < K).

var (
	dctOnce sync.Once
	dctMat  []float64 // dctMat[k*N+n] = c_k cos(pi (2n+1) k / 2N)
)

func dctMatrix() []float64 {
	dctOnce.Do(func() {
		dctMat = make([]float64, N*N)
		for k := 0; k < N; k++ {
			c := math.Sqrt(2.0 / N)
			if k == 0 {
				c = math.Sqrt(1.0 / N)
			}
			for n := 0; n < N; n++ {
				dctMat[k*N+n] = c * math.Cos(math.Pi*float64(2*n+1)*float64(k)/(2*N))
			}
		}
	})
	return dctMat
}

// dctForward returns the K×K low-frequency block of the 2-D DCT of x (N×N).
// out[u*K+v]: u = vertical frequency, v = horizontal frequency.
func dctForward(x []float64, K int) []float64 {
	m := dctMatrix()
	// rows: tmp[y*K+v] = sum_n x[y*N+n] m[v*N+n]
	tmp := make([]float64, N*K)
	parallel(N, func(y int) {
		row := x[y*N : y*N+N]
		for v := 0; v < K; v++ {
			mv := m[v*N : v*N+N]
			s := 0.0
			for n, xv := range row {
				s += xv * mv[n]
			}
			tmp[y*K+v] = s
		}
	})
	out := make([]float64, K*K)
	parallel(K, func(u int) {
		mu := m[u*N : u*N+N]
		o := out[u*K : u*K+K]
		for y := 0; y < N; y++ {
			c := mu[y]
			t := tmp[y*K : y*K+K]
			for v := range o {
				o[v] += c * t[v]
			}
		}
	})
	return out
}

// dctInverse turns a K×K low-frequency block back into an N×N image.
func dctInverse(c []float64, K int) []float64 {
	m := dctMatrix()
	// tmp[u*N+n] = sum_v c[u*K+v] m[v*N+n]
	tmp := make([]float64, K*N)
	parallel(K, func(u int) {
		t := tmp[u*N : u*N+N]
		for v := 0; v < K; v++ {
			cv := c[u*K+v]
			if cv == 0 {
				continue
			}
			mv := m[v*N : v*N+N]
			for n := range t {
				t[n] += cv * mv[n]
			}
		}
	})
	x := make([]float64, N*N)
	parallel(N, func(y int) {
		r := x[y*N : y*N+N]
		for u := 0; u < K; u++ {
			a := m[u*N+y]
			t := tmp[u*N : u*N+N]
			for n := range r {
				r[n] += a * t[n]
			}
		}
	})
	return x
}
