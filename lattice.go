package watermark

import (
	"math"
	"sort"
)

// Shared by the synchronization pattern: spectral analysis, tile lattice,
// folding, lattice search.

// ---- analysis of the (working-size) luma ----

type analysis struct {
	w, h, mw, mh int
	res          []float64 // whitened residual, w×h
	sqS          []float64 // sqrt of the smoothed power spectrum, mw×mh
	ratio        []float64 // P/S per bin, mw×mh (detection only)
	auto         []float64 // whitened autocorrelation, mw×mh (detection only)
}

func analyze(x []float64, w, h int, wantAuto bool) *analysis {
	mw, mh := nextPow2(w), nextPow2(h)
	mean := 0.0
	for _, v := range x {
		mean += v
	}
	mean /= float64(len(x))
	taper := func(i, n int) float64 {
		t := math.Min(float64(n)*0.04, 24)
		d := math.Min(float64(i)+0.5, float64(n)-float64(i)-0.5)
		if d >= t {
			return 1
		}
		return 0.5 - 0.5*math.Cos(math.Pi*d/t)
	}
	wx := make([]float64, w)
	for i := range wx {
		wx[i] = taper(i, w)
	}
	F := make([]complex128, mw*mh)
	parallel(h, func(y int) {
		wy := taper(y, h)
		for i := 0; i < w; i++ {
			F[y*mw+i] = complex((x[y*w+i]-mean)*wx[i]*wy, 0)
		}
	})
	fft2(F, mw, mh, false)
	P := make([]float64, mw*mh)
	for i, v := range F {
		P[i] = real(v)*real(v) + imag(v)*imag(v)
	}
	S := lineMax(P, boxWrap(P, mw, mh, 3), mw, mh, 5)
	a := &analysis{w: w, h: h, mw: mw, mh: mh, sqS: make([]float64, mw*mh)}
	inBand := func(i int) bool {
		fx := float64(signedBin(i%mw, mw)) / float64(mw)
		fy := float64(signedBin(i/mw, mh)) / float64(mh)
		r := math.Hypot(fx, fy)
		return r >= 0.004 && r <= 0.42
	}
	var Q []complex128
	if wantAuto {
		Q = make([]complex128, mw*mh)
	}
	for i := range F {
		s := math.Sqrt(S[i]) + 1e-9
		a.sqS[i] = s
		if wantAuto {
			if a.ratio == nil {
				a.ratio = make([]float64, mw*mh)
			}
			a.ratio[i] = P[i] / S[i]
		}
		if !inBand(i) {
			F[i] = 0
			continue
		}
		if Q != nil {
			Q[i] = complex(math.Min(math.Max(P[i]/S[i]-qFloor, 0), 40), 0)
		}
		F[i] /= complex(s, 0)
	}
	fft2(F, mw, mh, true)
	a.res = make([]float64, w*h)
	norm := 1.0 / float64(mw*mh)
	for y := 0; y < h; y++ {
		for i := 0; i < w; i++ {
			a.res[y*w+i] = real(F[y*mw+i]) * norm
		}
	}
	if Q != nil {
		fft2(Q, mw, mh, true)
		a.auto = make([]float64, mw*mh)
		for i, v := range Q {
			a.auto[i] = real(v)
		}
	}
	return a
}

func signedBin(i, n int) int {
	if i >= n/2 {
		return i - n
	}
	return i
}

// boxWrap is a (2r+1)² box mean with wrap-around.
func boxWrap(p []float64, w, h, r int) []float64 {
	tmp := make([]float64, w*h)
	out := make([]float64, w*h)
	n := float64(2*r + 1)
	parallel(h, func(y int) {
		s := 0.0
		for j := -r; j <= r; j++ {
			s += p[y*w+(j+w)%w]
		}
		for x := 0; x < w; x++ {
			tmp[y*w+x] = s / n
			s += p[y*w+(x+r+1)%w] - p[y*w+(x-r+w)%w]
		}
	})
	parallel(w, func(x int) {
		s := 0.0
		for j := -r; j <= r; j++ {
			s += tmp[((j+h)%h)*w+x]
		}
		for y := 0; y < h; y++ {
			out[y*w+x] = s / n
			s += tmp[((y+r+1)%h)*w+x] - tmp[((y-r+h)%h)*w+x]
		}
	})
	return out
}

// sqSAt returns sqrt(S) at the spatial frequency (fx, fy) in cycles/pixel.
func (a *analysis) sqSAt(fx, fy float64) float64 {
	bx := (int(math.Round(fx*float64(a.mw))) + 4*a.mw) % a.mw
	by := (int(math.Round(fy*float64(a.mh))) + 4*a.mh) % a.mh
	return a.sqS[by*a.mw+bx]
}

// ---- lattice ----

// lattice maps tile coordinates (in tiles) to analysis pixels:
// p = o + a·v + b·u.
type lattice struct {
	vx, vy, ux, uy float64
	ox, oy         float64
}

func (L lattice) det() float64 { return L.vx*L.uy - L.vy*L.ux }

func (L lattice) inv() (a, b, c, d float64) {
	det := L.det()
	return L.uy / det, -L.ux / det, -L.vy / det, L.vx / det
}

// fold averages res over all tiles of the lattice into one T×T tile
// (bilinear deposit), cells weighted by wgt (nil = all pixels).
func fold(a *analysis, L lattice) []float64 {
	i00, i01, i10, i11 := L.inv()
	const chunks = 16
	accs := make([][]float64, chunks)
	parallel(chunks, func(c int) {
		acc := make([]float64, 2*tileT*tileT)
		for y := c * a.h / chunks; y < (c+1)*a.h/chunks; y++ {
			dy := float64(y) - L.oy
			for x := 0; x < a.w; x++ {
				dx := float64(x) - L.ox
				ta := (i00*dx + i01*dy) * tileT
				tb := (i10*dx + i11*dy) * tileT
				fa, fb := math.Floor(ta), math.Floor(tb)
				ra, rb := ta-fa, tb-fb
				ia := (int(fa)%tileT + tileT) % tileT
				ib := (int(fb)%tileT + tileT) % tileT
				ia1, ib1 := (ia+1)%tileT, (ib+1)%tileT
				v := a.res[y*a.w+x]
				w00, w10, w01, w11 := (1-ra)*(1-rb), ra*(1-rb), (1-ra)*rb, ra*rb
				acc[ib*tileT+ia] += w00 * v
				acc[ib*tileT+ia1] += w10 * v
				acc[ib1*tileT+ia] += w01 * v
				acc[ib1*tileT+ia1] += w11 * v
				n := acc[tileT*tileT:]
				n[ib*tileT+ia] += w00
				n[ib*tileT+ia1] += w10
				n[ib1*tileT+ia] += w01
				n[ib1*tileT+ia1] += w11
			}
		}
		accs[c] = acc
	})
	t := make([]float64, tileT*tileT)
	mean := 0.0
	for i := range t {
		s, n := 0.0, 0.0
		for _, acc := range accs {
			s += acc[i]
			n += acc[tileT*tileT+i]
		}
		if n > 0 {
			t[i] = s / n
		}
		mean += t[i]
	}
	mean /= float64(len(t))
	for i := range t {
		t[i] -= mean
	}
	return t
}

// dihedral transforms of a T×T array: out[y][x] = in[src(x,y)].
// Bit 4: transpose, bit 1: flip x, bit 2: flip y (same codes as format 1).
func dihedralSrc(o, x, y int) (int, int) {
	sx, sy := x, y
	if o&1 != 0 {
		sx = (tileT - sx) % tileT
	}
	if o&2 != 0 {
		sy = (tileT - sy) % tileT
	}
	if o&4 != 0 {
		sx, sy = sy, sx
	}
	return sx, sy
}

// srcMatrix is the linear map of dihedralSrc on vectors.
func srcMatrix(o int) (a, b, c, d float64) {
	a, b, c, d = 1, 0, 0, 1
	if o&1 != 0 {
		a = -1
	}
	if o&2 != 0 {
		d = -1
	}
	if o&4 != 0 {
		a, b, c, d = 0, d, a, 0
	}
	return
}

func parabola(a, b, c float64) float64 {
	d := a - 2*b + c
	if d >= 0 {
		return 0
	}
	return math.Max(math.Min(0.5*(a-c)/d, 0.5), -0.5)
}

// ---- lattice search in the autocorrelation ----

type peak struct {
	x, y float64
	v    float64
}

func (a *analysis) autoAt(x, y float64) float64 {
	fx, fy := math.Floor(x), math.Floor(y)
	rx, ry := x-fx, y-fy
	g := func(i, j int) float64 {
		return a.auto[((j%a.mh+a.mh)%a.mh)*a.mw+(i%a.mw+a.mw)%a.mw]
	}
	ix, iy := int(fx), int(fy)
	return (1-rx)*(1-ry)*g(ix, iy) + rx*(1-ry)*g(ix+1, iy) + (1-rx)*ry*g(ix, iy+1) + rx*ry*g(ix+1, iy+1)
}

// findLattices returns candidate tile lattices, best first.
func findLattices(a *analysis, minP float64) []lattice {
	// normalize by the spread of the autocorrelation in the search area
	limX, limY := float64(min(a.w, a.mw/2))*0.45, float64(min(a.h, a.mh/2))*0.45
	inArea := func(x, y float64) bool {
		r := math.Hypot(x, y)
		return r >= minP && math.Abs(x) <= limX && math.Abs(y) <= limY
	}
	var vals []float64
	var peaks []peak
	for j := 0; j < a.mh; j++ {
		y := signedBin(j, a.mh)
		for i := 0; i < a.mw; i++ {
			x := signedBin(i, a.mw)
			if y < 0 || (y == 0 && x <= 0) || !inArea(float64(x), float64(y)) {
				continue
			}
			v := a.auto[j*a.mw+i]
			vals = append(vals, v)
			ismax := true
			for dy := -1; dy <= 1 && ismax; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if (dx != 0 || dy != 0) && a.autoAt(float64(x+dx), float64(y+dy)) > v {
						ismax = false
						break
					}
				}
			}
			if ismax {
				peaks = append(peaks, peak{float64(x), float64(y), v})
			}
		}
	}
	if len(vals) < 10 || len(peaks) < 2 {
		return nil
	}
	sort.Float64s(vals)
	med := vals[len(vals)/2]
	mad := vals[len(vals)*3/4] - vals[len(vals)/4] + 1e-12
	norm := func(v float64) float64 { return (v - med) / mad }
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].v > peaks[j].v })
	if len(peaks) > 30 {
		peaks = peaks[:30]
	}
	// candidate vectors: both signs of each peak
	var vecs []peak
	for _, p := range peaks {
		vecs = append(vecs, p, peak{-p.x, -p.y, p.v})
	}
	type cand struct {
		L     lattice
		score float64
		area  float64
	}
	var cands []cand
	for i, v := range vecs {
		for _, u := range vecs[i+1:] {
			det := v.x*u.y - v.y*u.x
			lv, lu := math.Hypot(v.x, v.y), math.Hypot(u.x, u.y)
			if det <= 0 {
				continue
			}
			cos := (v.x*u.x + v.y*u.y) / (lv * lu)
			if math.Abs(cos) > 0.55 || lu/lv > 2.5 || lv/lu > 2.5 {
				continue
			}
			s, n := 0.0, 0
			for ii := -4; ii <= 4; ii++ {
				for jj := -4; jj <= 4; jj++ {
					if ii == 0 && jj == 0 {
						continue
					}
					x, y := float64(ii)*v.x+float64(jj)*u.x, float64(ii)*v.y+float64(jj)*u.y
					if !inArea(x, y) {
						continue
					}
					s += norm(a.autoAt(x, y))
					n++
				}
			}
			if n < 4 {
				continue
			}
			cands = append(cands, cand{reduceBasis(lattice{vx: v.x, vy: v.y, ux: u.x, uy: u.y}), s / math.Sqrt(float64(n)), det})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	// the score is a sum over the lattice points / sqrt(count): a coarser
	// sublattice or a finer wrong lattice both score lower than the true one
	var out []lattice
	for len(out) < maxLattices && len(cands) > 0 {
		bi := 0
		L := cands[bi].L
		out = append(out, refineLattice(a, L, inArea))
		// drop candidates that describe the same lattice
		var rest []cand
		for _, c := range cands {
			if !sameLattice(c.L, L) {
				rest = append(rest, c)
			}
		}
		cands = rest
	}
	return out
}

// sameLattice: v and u of b lie on the lattice a and the areas match.
func sameLattice(a, b lattice) bool {
	if math.Abs(math.Abs(a.det())-math.Abs(b.det())) > 0.1*math.Abs(a.det()) {
		return false
	}
	i00, i01, i10, i11 := a.inv()
	on := func(x, y float64) bool {
		p, q := i00*x+i01*y, i10*x+i11*y
		return math.Abs(p-math.Round(p)) < 0.15 && math.Abs(q-math.Round(q)) < 0.15
	}
	return on(b.vx, b.vy) && on(b.ux, b.uy)
}

// refineLattice fits v and u to the sub-pixel peak positions of the
// lattice points (least squares, weighted by peak height).
func refineLattice(a *analysis, L lattice, inArea func(x, y float64) bool) lattice {
	for iter := 0; iter < 2; iter++ {
		var sii, sij, sjj, sixx, sjxx, sixy, sjxy float64
		for ii := -6; ii <= 6; ii++ {
			for jj := -6; jj <= 6; jj++ {
				if ii == 0 && jj == 0 {
					continue
				}
				x0, y0 := float64(ii)*L.vx+float64(jj)*L.ux, float64(ii)*L.vy+float64(jj)*L.uy
				if !inArea(x0, y0) {
					continue
				}
				bx, by, bv := math.Round(x0), math.Round(y0), math.Inf(-1)
				for dy := -2.0; dy <= 2; dy++ {
					for dx := -2.0; dx <= 2; dx++ {
						if v := a.autoAt(math.Round(x0)+dx, math.Round(y0)+dy); v > bv {
							bx, by, bv = math.Round(x0)+dx, math.Round(y0)+dy, v
						}
					}
				}
				if bv <= 0 {
					continue
				}
				px := bx + parabola(a.autoAt(bx-1, by), bv, a.autoAt(bx+1, by))
				py := by + parabola(a.autoAt(bx, by-1), bv, a.autoAt(bx, by+1))
				w := bv
				fi, fj := float64(ii), float64(jj)
				sii += w * fi * fi
				sij += w * fi * fj
				sjj += w * fj * fj
				sixx += w * fi * px
				sjxx += w * fj * px
				sixy += w * fi * py
				sjxy += w * fj * py
			}
		}
		det := sii*sjj - sij*sij
		if det <= 1e-9 {
			return L
		}
		nL := L
		nL.vx = (sjj*sixx - sij*sjxx) / det
		nL.ux = (sii*sjxx - sij*sixx) / det
		nL.vy = (sjj*sixy - sij*sjxy) / det
		nL.uy = (sii*sjxy - sij*sixy) / det
		if math.Hypot(nL.vx-L.vx, nL.vy-L.vy) > 0.1*math.Hypot(L.vx, L.vy) {
			return L
		}
		L = nL
	}
	return L
}

// ---- detection ----

func workSize(W, H int) (int, int, float64) {
	s := math.Min(1, float64(workMax)/float64(max(W, H)))
	return max(int(math.Round(float64(W)*s)), 1), max(int(math.Round(float64(H)*s)), 1), s
}

// makeMaskWH is makeMask for a w×h image.
func makeMaskWH(x []float64, w, h int) []float64 {
	const c, r = 3.0, 2
	m := make([]float64, w*h)
	parallel(h, func(y int) {
		for i := 0; i < w; i++ {
			s1, s2, n := 0.0, 0.0, 0.0
			for yy := max(y-r, 0); yy <= min(y+r, h-1); yy++ {
				for xx := max(i-r, 0); xx <= min(i+r, w-1); xx++ {
					v := x[yy*w+xx]
					s1 += v
					s2 += v * v
					n++
				}
			}
			sd := math.Sqrt(math.Max(s2/n-(s1/n)*(s1/n), 0))
			m[y*w+i] = sd / (sd + c)
		}
	})
	out := make([]float64, w*h)
	parallel(h, func(y int) {
		for i := 0; i < w; i++ {
			v := math.Inf(1)
			for yy := max(y-1, 0); yy <= min(y+1, h-1); yy++ {
				for xx := max(i-1, 0); xx <= min(i+1, w-1); xx++ {
					v = math.Min(v, m[yy*w+xx])
				}
			}
			out[y*w+i] = v
		}
	})
	return out
}

// lineMax raises the smoothed spectrum S to the mean of P along short
// segments in eight directions (±r bins). A straight line in the spectrum
// (from an edge of the image, a rotated frame or a collage border) is
// smooth along its own direction and thus no longer counts as a peak;
// isolated peaks (the tile lattice) are diluted in every direction.
func lineMax(P, S []float64, w, h, r int) []float64 {
	out := make([]float64, len(S))
	var dirs [8][2]float64
	for d := range dirs {
		a := float64(d) * math.Pi / 8
		dirs[d] = [2]float64{math.Cos(a), math.Sin(a)}
	}
	parallel(h, func(y int) {
		for x := 0; x < w; x++ {
			m := S[y*w+x]
			for _, d := range dirs {
				s := 0.0
				for t := -r; t <= r; t++ {
					xx := (x + int(math.Round(float64(t)*d[0])) + w) % w
					yy := (y + int(math.Round(float64(t)*d[1])) + h) % h
					s += P[yy*w+xx]
				}
				m = math.Max(m, s/float64(2*r+1))
			}
			out[y*w+x] = m
		}
	})
	return out
}

// qFloor: spectrum bins count as peaks above qFloor × the local level
// (host bins exceed 4 with probability e^-4 ≈ 2 %).
var qFloor = 4.0

// maxLattices candidate lattices are checked against the sync pattern.
const maxLattices = 12

// reduceBasis returns the Lagrange-reduced basis of the same lattice (the
// two shortest vectors, nearly orthogonal): the tile axes rather than a
// sheared pair such as (v, u+v).
func reduceBasis(L lattice) lattice {
	for i := 0; i < 20; i++ {
		if L.ux*L.ux+L.uy*L.uy < L.vx*L.vx+L.vy*L.vy {
			L.vx, L.vy, L.ux, L.uy = L.ux, L.uy, L.vx, L.vy
		}
		m := math.Round((L.ux*L.vx + L.uy*L.vy) / (L.vx*L.vx + L.vy*L.vy))
		if m == 0 {
			break
		}
		L.ux -= m * L.vx
		L.uy -= m * L.vy
	}
	if L.det() < 0 {
		L.ux, L.uy = -L.ux, -L.uy
	}
	return L
}
