package watermark

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/cmplx"
	"math/rand/v2"
	"sort"
)

// Synchronization pattern: makes the watermark readable after cropping,
// rotation by any angle and pasting into a collage.
//
// The id itself stays in the global DCT watermark on the canonical 512×512
// grid (wm.go). That grid is tied to the image frame, so any change of the
// frame (crop, free rotation, collage) moves the carriers. The sync pattern
// marks the frame: the image is divided into 8×8 tiles (W/8 × H/8 pixels
// each, origin top left), and every tile gets the same faint, key-dependent
// pattern (a sum of ~40 sinusoids with 10–28 cycles per tile).
//
// Detection when the plain reading fails:
//
//  1. Spectral whitening of the suspect's luma. The repetition shows up as
//     peaks of the whitened autocorrelation on the tile lattice; its two
//     shortest vectors give rotation, scale and shear.
//  2. The whitened image is folded along that lattice into one tile; a
//     cyclic correlation with the known pattern (via FFT, eight dihedral
//     variants) gives the tile phase and orientation. The lattice is then
//     refined by maximizing that correlation.
//  3. Per tile the pattern's local correlation shows which tiles carry the
//     watermark; the best 8×8 windows are the candidate frames.
//  4. For each candidate the canonical 512×512 grid is sampled straight
//     from the suspect (one tile = 64 grid pixels) and read with the
//     normal detector. Missing parts (cropped away) are filled with the
//     mean; the id survives as long as most of the frame is there.

const (
	tileT         = 64   // cells per tile side when folding (= grid pixels per tile)
	tileL         = 256  // synthesis resolution of one tile
	tilesFrame    = 8    // tiles per frame side: 8 × 64 = N
	syncFLo       = 10.0 // band of the pattern in cycles per tile
	syncFHi       = 28.0
	syncCount     = 40   // sinusoids (half plane)
	noiseFLo      = 6.0  // band of the noise reference
	noiseFHi      = 31.0 //
	syncThreshold = 7.0  // correlation (z) that marks a lattice as ours
	fUsable       = 0.30 // highest usable frequency in cycles per analysis pixel
	workMax       = 1024 // long side of the analysis image
	workMaxWindow = 640  // the same for the windows
	maxFrames     = 36   // candidate frames read per lattice
)

type syncLayout struct {
	k, l  []int
	ph    []complex128 // e^{iφ} per sinusoid
	ref   []complex128 // T×T: e^{iφ} at the sinusoids (half plane)
	noise []int        // T×T indices of the noise reference
}

func newSyncLayout(key []byte, namespace string) *syncLayout {
	seed := sha256.Sum256(append([]byte(namespace+"/sync1\x00"), key...))
	rng := rand.New(rand.NewChaCha8(seed))
	type kl struct{ k, l int }
	var band []kl
	for l := 0; l < tileT/2; l++ {
		for k := -tileT/2 + 1; k < tileT/2; k++ {
			if l == 0 && k <= 0 {
				continue
			}
			if r := math.Hypot(float64(k), float64(l)); r >= syncFLo && r <= syncFHi {
				band = append(band, kl{k, l})
			}
		}
	}
	rng.Shuffle(len(band), func(i, j int) { band[i], band[j] = band[j], band[i] })
	sl := &syncLayout{ref: make([]complex128, tileT*tileT)}
	for _, f := range band[:syncCount] {
		ph := cmplx.Rect(1, 2*math.Pi*rng.Float64())
		sl.k = append(sl.k, f.k)
		sl.l = append(sl.l, f.l)
		sl.ph = append(sl.ph, ph)
		sl.ref[specIdx(f.k, f.l)] = ph
	}
	for l := 0; l < tileT/2; l++ {
		for k := -tileT/2 + 1; k < tileT/2; k++ {
			if l == 0 && k <= 0 {
				continue
			}
			r := math.Hypot(float64(k), float64(l))
			if r >= noiseFLo && r <= noiseFHi && sl.ref[specIdx(k, l)] == 0 {
				sl.noise = append(sl.noise, specIdx(k, l))
			}
		}
	}
	return sl
}

func specIdx(k, l int) int { return ((l+tileT)%tileT)*tileT + (k+tileT)%tileT }

// pattern value at tile cell (x, y) for unit amplitude.
func (sl *syncLayout) at(x, y float64) float64 {
	v := 0.0
	for i := range sl.k {
		v += real(sl.ph[i] * cmplx.Rect(1, 2*math.Pi*(float64(sl.k[i])*x+float64(sl.l[i])*y)/tileT))
	}
	return v
}

type syncRead struct {
	o      int
	sx, sy float64 // tile phase in cells
	z      float64 // correlation with the pattern, ~N(0,1) without it
	amp    float64 // mean projection on the pattern (fold units)
	sigma  float64 // noise per coefficient (fold units)
}

// usableMask marks fold coefficients whose spatial frequency stays below
// fUsable (higher ones are lost to downscaling).
func usableMask(L lattice) []bool {
	i00, i01, i10, i11 := L.inv()
	u := make([]bool, tileT*tileT)
	for l := 0; l < tileT; l++ {
		for k := 0; k < tileT; k++ {
			kk, ll := float64(signedBin(k, tileT)), float64(signedBin(l, tileT))
			fx := i00*kk + i10*ll
			fy := i01*kk + i11*ll
			u[l*tileT+k] = math.Abs(fx) <= fUsable && math.Abs(fy) <= fUsable
		}
	}
	return u
}

// readSync finds orientation and phase of the folded tile.
func readSync(t []float64, L lattice, sl *syncLayout, fixedO int) syncRead {
	usable := usableMask(L)
	best := syncRead{z: math.Inf(-1)}
	for o := 0; o < 8; o++ {
		if fixedO >= 0 && o != fixedO {
			continue
		}
		C := make([]complex128, tileT*tileT)
		use := make([]bool, tileT*tileT)
		for y := 0; y < tileT; y++ {
			for x := 0; x < tileT; x++ {
				sx, sy := dihedralSrc(o, x, y)
				C[y*tileT+x] = complex(t[sy*tileT+sx], 0)
				use[y*tileT+x] = usable[sy*tileT+sx]
			}
		}
		fft2(C, tileT, tileT, false)
		for i := range C {
			C[i] *= complex(2.0/(tileT*tileT), 0)
			if !use[i] {
				C[i] = 0
			}
		}
		var r syncRead
		if fixedO >= 0 {
			r = scoreSync(C, 0, 0, sl)
		} else {
			G := make([]complex128, tileT*tileT)
			for i, p := range sl.ref {
				if p != 0 {
					G[i] = C[i] * cmplx.Conj(p)
				}
			}
			fft2(G, tileT, tileT, false)
			bi := 0
			for i := range G {
				if real(G[i]) > real(G[bi]) {
					bi = i
				}
			}
			at := func(x, y int) float64 { return real(G[((y+tileT)%tileT)*tileT+(x+tileT)%tileT]) }
			px, py := bi%tileT, bi/tileT
			sx := float64(px) + parabola(at(px-1, py), at(px, py), at(px+1, py))
			sy := float64(py) + parabola(at(px, py-1), at(px, py), at(px, py+1))
			r = scoreSync(C, sx, sy, sl)
		}
		r.o = o
		if r.z > best.z {
			best = r
		}
	}
	return best
}

// scoreSync correlates the spectrum (shifted back by sx, sy) with the
// pattern; the noise level comes from the other coefficients of the band.
func scoreSync(C []complex128, sx, sy float64, sl *syncLayout) syncRead {
	n, nn := 0, 0
	num, sq := 0.0, 0.0
	for i := range sl.k {
		c := C[specIdx(sl.k[i], sl.l[i])]
		if c == 0 {
			continue
		}
		ph := -2 * math.Pi * (float64(sl.k[i])*sx + float64(sl.l[i])*sy) / tileT
		num += real(c * cmplx.Rect(1, ph) * cmplx.Conj(sl.ph[i]))
		n++
	}
	for _, i := range sl.noise {
		if c := C[i]; c != 0 {
			sq += real(c)*real(c) + imag(c)*imag(c)
			nn++
		}
	}
	r := syncRead{sx: sx, sy: sy}
	if n == 0 || nn < 10 {
		return syncRead{z: math.Inf(-1)}
	}
	r.sigma = math.Sqrt(sq/float64(nn)/2) + 1e-12
	r.amp = num / float64(n)
	r.z = num / (r.sigma * math.Sqrt(float64(n)))
	return r
}

// improveSync refines the lattice by a pattern search on the correlation.
func improveSync(a *analysis, L lattice, r syncRead, sl *syncLayout) (lattice, syncRead) {
	step := 0.003
	for it := 0; it < 16 && step > 0.0004; it++ {
		better := false
		for p := 0; p < 4; p++ {
			for _, sgn := range []float64{1, -1} {
				n := L
				d := sgn * step * math.Sqrt(math.Abs(L.det()))
				switch p {
				case 0:
					n.vx += d
				case 1:
					n.vy += d
				case 2:
					n.ux += d
				case 3:
					n.uy += d
				}
				if nr := readSync(fold(a, n), n, sl, -1); nr.z > r.z {
					L, r, better = n, nr, true
				}
			}
		}
		if !better {
			step /= 2
		}
	}
	return L, r
}

// geometry of a found pattern: analysis pixel p = M·(S·(τ - s/T) + n) for
// original tile coordinates τ (in tiles), integer frame offset n.
type syncGeom struct {
	L          lattice
	r          syncRead
	a, b, c, d float64 // S = srcMatrix(o)
}

func (g syncGeom) toPixel(tx, ty float64) (float64, float64) {
	tx -= g.r.sx / tileT
	ty -= g.r.sy / tileT
	fx, fy := g.a*tx+g.b*ty, g.c*tx+g.d*ty
	return g.L.ox + fx*g.L.vx + fy*g.L.ux, g.L.oy + fx*g.L.vy + fy*g.L.uy
}

func (g syncGeom) toTile(px, py float64) (float64, float64) {
	i00, i01, i10, i11 := g.L.inv()
	dx, dy := px-g.L.ox, py-g.L.oy
	fx, fy := i00*dx+i01*dy, i10*dx+i11*dy
	// S is a signed permutation: S⁻¹ = Sᵀ
	return g.a*fx + g.c*fy + g.r.sx/tileT, g.b*fx + g.d*fy + g.r.sy/tileT
}

// frameCandidates scores every tile by its local correlation with the
// pattern and returns the origins (in tiles) of the best 8×8 windows.
func frameCandidates(a *analysis, g syncGeom, sl *syncLayout) [][2]int {
	// reference pattern on a fine grid of one tile
	const R = 128
	ref := make([]float64, R*R)
	parallel(R, func(y int) {
		for x := 0; x < R; x++ {
			ref[y*R+x] = sl.at(float64(x)*tileT/R, float64(y)*tileT/R)
		}
	})
	minX, minY, maxX, maxY := math.MaxInt32, math.MaxInt32, math.MinInt32, math.MinInt32
	for _, c := range [][2]float64{{0, 0}, {float64(a.w), 0}, {0, float64(a.h)}, {float64(a.w), float64(a.h)}} {
		tx, ty := g.toTile(c[0], c[1])
		minX, maxX = min(minX, int(math.Floor(tx))), max(maxX, int(math.Floor(tx)))
		minY, maxY = min(minY, int(math.Floor(ty))), max(maxY, int(math.Floor(ty)))
	}
	gw, gh := maxX-minX+1, maxY-minY+1
	if gw*gh > 4096 {
		return nil
	}
	const chunks = 16
	parts := make([][]float64, chunks)
	parallel(chunks, func(c int) {
		sc := make([]float64, gw*gh)
		for y := c * a.h / chunks; y < (c+1)*a.h/chunks; y++ {
			for x := 0; x < a.w; x++ {
				tx, ty := g.toTile(float64(x), float64(y))
				ix, iy := int(math.Floor(tx)), int(math.Floor(ty))
				rx := (tx - float64(ix)) * R
				ry := (ty - float64(iy)) * R
				jx, jy := int(rx)%R, int(ry)%R
				sc[(iy-minY)*gw+ix-minX] += a.res[y*a.w+x] * ref[jy*R+jx]
			}
		}
		parts[c] = sc
	})
	score := make([]float64, gw*gh)
	for _, p := range parts {
		for i, v := range p {
			score[i] += v
		}
	}
	type win struct {
		x, y int
		s    float64
	}
	var wins []win
	for oy := minY - tilesFrame + 1; oy <= maxY; oy++ {
		for ox := minX - tilesFrame + 1; ox <= maxX; ox++ {
			s := 0.0
			for ty := max(oy, minY); ty < min(oy+tilesFrame, maxY+1); ty++ {
				for tx := max(ox, minX); tx < min(ox+tilesFrame, maxX+1); tx++ {
					s += score[(ty-minY)*gw+tx-minX]
				}
			}
			wins = append(wins, win{ox, oy, s})
		}
	}
	sort.SliceStable(wins, func(i, j int) bool { return wins[i].s > wins[j].s })
	var out [][2]int
	for _, w := range wins {
		if len(out) >= maxFrames || w.s < 0.5*wins[0].s {
			break
		}
		out = append(out, [2]int{w.x, w.y})
	}
	return out
}

// reconstruct samples the canonical N×N grid of the frame with origin
// (nx, ny) tiles from the analysis luma x. Returns nil if less than a
// quarter of the frame is inside the image.
func reconstruct(x []float64, w, h int, g syncGeom, nx, ny int) []float64 {
	out := make([]float64, N*N)
	inside := make([]bool, N*N)
	parallel(N, func(cy int) {
		for cx := 0; cx < N; cx++ {
			tx := float64(nx) + (float64(cx)+0.5)/tileT
			ty := float64(ny) + (float64(cy)+0.5)/tileT
			px, py := g.toPixel(tx, ty)
			if px < 0 || py < 0 || px > float64(w-1) || py > float64(h-1) {
				continue
			}
			x0, y0 := int(px), int(py)
			x1, y1 := min(x0+1, w-1), min(y0+1, h-1)
			rx, ry := px-float64(x0), py-float64(y0)
			out[cy*N+cx] = (1-rx)*(1-ry)*x[y0*w+x0] + rx*(1-ry)*x[y0*w+x1] + (1-rx)*ry*x[y1*w+x0] + rx*ry*x[y1*w+x1]
			inside[cy*N+cx] = true
		}
	})
	n := 0
	for _, in := range inside {
		if in {
			n++
		}
	}
	if n < N*N/4 {
		return nil
	}
	smoothFill(out, inside)
	return out
}

// smoothFill continues the image into the missing pixels: block means on a
// coarse grid, diffused into empty blocks, then interpolated. A hard edge
// to a flat fill would add strong spurious coefficients to the band.
func smoothFill(x []float64, inside []bool) {
	const B = 16 // block size, N/B blocks per side
	const G = N / B
	sum := make([]float64, G*G)
	cnt := make([]float64, G*G)
	for i, in := range inside {
		if in {
			g := (i/N/B)*G + (i%N)/B
			sum[g] += x[i]
			cnt[g]++
		}
	}
	val := make([]float64, G*G)
	known := make([]bool, G*G)
	mean, mn := 0.0, 0.0
	for g := range val {
		if cnt[g] > B*B/4 {
			val[g] = sum[g] / cnt[g]
			known[g] = true
			mean += val[g]
			mn++
		}
	}
	for g := range val {
		if !known[g] {
			val[g] = mean / math.Max(mn, 1)
		}
	}
	for it := 0; it < 200; it++ {
		nv := append([]float64(nil), val...)
		for gy := 0; gy < G; gy++ {
			for gx := 0; gx < G; gx++ {
				g := gy*G + gx
				if known[g] {
					continue
				}
				s, c := 0.0, 0.0
				for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
					yy, xx := gy+d[1], gx+d[0]
					if yy >= 0 && yy < G && xx >= 0 && xx < G {
						s += val[yy*G+xx]
						c++
					}
				}
				nv[g] = s / c
			}
		}
		val = nv
	}
	for y := 0; y < N; y++ {
		fy := math.Min(math.Max((float64(y)+0.5)/B-0.5, 0), G-1)
		y0 := int(fy)
		y1 := min(y0+1, G-1)
		ry := fy - float64(y0)
		for i := 0; i < N; i++ {
			if inside[y*N+i] {
				continue
			}
			fx := math.Min(math.Max((float64(i)+0.5)/B-0.5, 0), G-1)
			x0 := int(fx)
			x1 := min(x0+1, G-1)
			rx := fx - float64(x0)
			x[y*N+i] = (1-rx)*(1-ry)*val[y0*G+x0] + rx*(1-ry)*val[y0*G+x1] + (1-rx)*ry*val[y1*G+x0] + rx*ry*val[y1*G+x1]
		}
	}
}

// detectSynced looks for the sync pattern and reads the id from the
// reconstructed frame: first in the whole image, then in windows (a pasted
// picture fills a window better than the whole collage).
func detectSynced(wi *workImage, ls layouts, verbose bool) (Result, bool) {
	var best Result
	have := false
	for _, w := range syncWindows(wi.W, wi.H) {
		r, ok := detectSyncedIn(wi, ls, w, verbose)
		if ok && (!have || r.Found && !best.Found || r.Found == best.Found && r.SyncConfidence > best.SyncConfidence) {
			best, have = r, true
		}
		if best.Found {
			break
		}
	}
	return best, have
}

// syncWindows: the whole image, 2/3 windows in the corners, 1/2 windows on
// a 3×3 grid (x, y, w, h in pixels).
func syncWindows(W, H int) [][4]int {
	out := [][4]int{{0, 0, W, H}}
	for _, f := range []struct {
		size  float64
		steps []float64
	}{{2.0 / 3, []float64{0, 1.0 / 3}}, {0.5, []float64{0, 0.25, 0.5}}} {
		w, h := int(float64(W)*f.size), int(float64(H)*f.size)
		if min(w, h) < 160 {
			continue
		}
		for _, fy := range f.steps {
			for _, fx := range f.steps {
				out = append(out, [4]int{int(float64(W) * fx), int(float64(H) * fy), w, h})
			}
		}
	}
	return out
}

func detectSyncedIn(wi *workImage, ls layouts, win [4]int, verbose bool) (Result, bool) {
	ww, wh, s := workSize(win[2], win[3])
	if win[2] < wi.W || win[3] < wi.H {
		// windows: smaller analysis, the pasted picture is smaller as well
		f := math.Min(1, workMaxWindow/float64(max(ww, wh)))
		s *= f
		ww, wh = max(int(math.Round(float64(win[2])*s)), 1), max(int(math.Round(float64(win[3])*s)), 1)
	}
	a := analyze(wi.resampleRegion(win[0], win[1], win[2], win[3], ww, wh), ww, wh, true)
	var best Result
	have := false
	type lc struct {
		L lattice
		r syncRead
	}
	var lcs []lc
	cands := findLattices(a, 12)
	if win[2] == wi.W && win[3] == wi.H {
		cands = append(cands, combLattices(a, ls.sync)...)
	}
	for _, L := range cands {
		lcs = append(lcs, lc{L, readSync(fold(a, L), L, ls.sync, -1)})
	}
	sort.SliceStable(lcs, func(i, j int) bool { return lcs[i].r.z > lcs[j].r.z })
	var full []float64 // luma of the whole image at the window's scale
	fw, fh := int(math.Round(float64(wi.W)*s)), int(math.Round(float64(wi.H)*s))
	for i, c := range lcs {
		L, r := c.L, c.r
		if verbose && i < 3 {
			fmt.Printf("window %v lattice v=(%.2f,%.2f) u=(%.2f,%.2f): sync z %.2f\n", win, L.vx, L.vy, L.ux, L.uy, r.z)
		}
		if i >= 2 || r.z < syncThreshold*0.7 {
			continue
		}
		L, r = improveSync(a, L, r, ls.sync)
		if verbose {
			fmt.Printf("  refined v=(%.3f,%.3f) u=(%.3f,%.3f): sync z %.2f, orientation %d, phase %.2f,%.2f\n",
				L.vx, L.vy, L.ux, L.uy, r.z, r.o, r.sx, r.sy)
		}
		if r.z < syncThreshold {
			continue
		}
		g := syncGeom{L: L, r: r}
		g.a, g.b, g.c, g.d = srcMatrix(r.o)
		// the same geometry in the whole image (window offset)
		gf := g
		gf.L.ox += float64(win[0]) * s
		gf.L.oy += float64(win[1]) * s
		if full == nil {
			full = wi.resampleLuma(fw, fh)
		}
		// frame size in pixels of the suspect: limits the usable frequencies
		ex, ey := g.toPixel(tilesFrame, 0)
		ox, oy := g.toPixel(0, 0)
		qx, qy := g.toPixel(0, tilesFrame)
		uMax := int(math.Hypot(qx-ox, qy-oy) / s * 0.55)
		vMax := int(math.Hypot(ex-ox, ey-oy) / s * 0.55)
		for _, c := range frameCandidates(a, g, ls.sync) {
			// gf maps the same tile coordinates into the whole image
			grid := reconstruct(full, fw, fh, gf, c[0], c[1])
			if grid == nil {
				continue
			}
			z := slotScores(grid, ls.v1, uMax, vMax)
			id, flips, ok := decodeBits(z[:])
			for k := dataSlots; k < slots; k++ {
				if z[k] <= 0 {
					ok = false
				}
			}
			p := presence(z)
			ok = ok && p >= thresholdFound
			if verbose {
				fmt.Printf("  frame %v: presence %.2f ok %v\n", c, p, ok)
			}
			if !have || ok && !best.Found || ok == best.Found && p > best.Confidence {
				minZ := math.Inf(1)
				for k := 0; k < dataSlots; k++ {
					minZ = math.Min(minZ, math.Abs(z[k]))
				}
				best = Result{
					Status: "none", Found: ok, Confidence: round2(p), CRCOK: ok, Corrected: flips,
					MinBitZ: round2(minZ), Width: wi.W, Height: wi.H,
					Synced: true, SyncConfidence: round2(r.z),
				}
				best.Orientation, best.Rotation = tileOrientation(g)
				if ok {
					best.Status = "found"
					best.ID = &id
				} else if p >= thresholdPresent {
					best.Status = "present_unreadable"
				}
				if verbose {
					for _, v := range z {
						best.BitZ = append(best.BitZ, round2(v))
					}
				}
				have = true
			}
			if best.Found {
				return best, true
			}
		}
		if !have {
			best = Result{Status: "present_unreadable", Width: wi.W, Height: wi.H, Synced: true, SyncConfidence: round2(r.z)}
			best.Orientation, best.Rotation = tileOrientation(g)
			have = true
		}
	}
	return best, have
}

// combLattices finds axis-parallel tile lattices of a cropped (not
// rotated, not scaled) image without the autocorrelation: for every tile
// size that a crop of up to 40 % per axis allows, it sums the spectral
// peakiness P/S exactly at the 40 known pattern frequencies. Periodic
// structures of the image itself (text lines, UI grids) that mislead the
// autocorrelation hardly hit those frequencies.
func combLattices(a *analysis, sl *syncLayout) []lattice {
	if a.ratio == nil {
		return nil
	}
	at := func(fx, fy float64) float64 {
		x, y := fx*float64(a.mw), fy*float64(a.mh)
		x0, y0 := int(math.Floor(x)), int(math.Floor(y))
		m := 0.0
		for dy := 0; dy <= 1; dy++ {
			for dx := 0; dx <= 1; dx++ {
				i := (((y0+dy)%a.mh+a.mh)%a.mh)*a.mw + ((x0+dx)%a.mw+a.mw)%a.mw
				m = math.Max(m, a.ratio[i])
			}
		}
		return math.Min(m, 30)
	}
	const step = 1.0025
	w0, h0 := float64(a.w)/tilesFrame, float64(a.h)/tilesFrame
	var tws, ths []float64
	for t := w0; t <= w0/0.6; t *= step {
		tws = append(tws, t)
	}
	for t := h0; t <= h0/0.6; t *= step {
		ths = append(ths, t)
	}
	type hit struct {
		tw, th, z float64
	}
	var hits []hit
	n := float64(len(sl.k))
	// four variants: identity, mirrored, transposed, both (a full
	// negation does not change |F|)
	for o := 0; o < 4; o++ {
		ks, ls := make([]float64, len(sl.k)), make([]float64, len(sl.k))
		for i := range sl.k {
			k, l := float64(sl.k[i]), float64(sl.l[i])
			if o&1 != 0 {
				k = -k
			}
			if o&2 != 0 {
				k, l = l, k
			}
			ks[i], ls[i] = k, l
		}
		best := make([]hit, len(tws))
		parallel(len(tws), func(i int) {
			b := hit{z: math.Inf(-1)}
			for _, th := range ths {
				sum := 0.0
				for j := range ks {
					sum += at(ks[j]/tws[i], ls[j]/th)
				}
				if z := (sum - n) / math.Sqrt(n); z > b.z {
					b = hit{tws[i], th, z}
				}
			}
			best[i] = b
		})
		for _, b := range best {
			hits = append(hits, b)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].z > hits[j].z })
	var out []lattice
	for _, h := range hits {
		if len(out) >= 3 {
			break
		}
		dup := false
		for _, L := range out {
			if math.Abs(L.vx/h.tw-1) < 0.01 && math.Abs(L.uy/h.th-1) < 0.01 {
				dup = true
			}
		}
		if !dup {
			out = append(out, lattice{vx: h.tw, uy: h.th})
		}
	}
	return out
}

// tileOrientation describes how the original frame appears in the image:
// one of the eight names of format 1 (nearest multiple of 90°) and the
// rotation angle in degrees clockwise (0 if it is that multiple).
func tileOrientation(g syncGeom) (string, float64) {
	ox, oy := g.toPixel(0, 0)
	ex, ey := g.toPixel(1, 0)
	fx, fy := g.toPixel(0, 1)
	ex, ey, fx, fy = ex-ox, ey-oy, fx-ox, fy-oy
	mirrored := ex*fy-ey*fx < 0
	deg := math.Atan2(ey, ex) * 180 / math.Pi
	q := int(math.Round(deg/90)+4) % 4 // 0: right, 1: down, 2: left, 3: up
	names := [2][4]string{
		{"none", "rotated 90° clockwise", "rotated 180°", "rotated 90° counter-clockwise"},
		{"mirrored vertically", "transposed", "mirrored horizontally", "transverse"},
	}
	m := 0
	if mirrored {
		m = 1
	}
	rot := 0.0
	if math.Abs(deg-math.Round(deg/90)*90) >= 0.5 {
		rot = round2(deg)
	}
	return names[m][q], rot
}

// ---- embedding ----

// syncAmp is the luma amplitude of each sinusoid (0..255 scale, before the mask).
var syncAmp = 0.4

// frameLattice is the tile lattice of an unmodified W×H image in analysis
// pixels.
func frameLattice(W, H int) (lattice, int, int, float64) {
	ww, wh, s := workSize(W, H)
	off := -(0.5 - 0.5*s) // analysis pixel i sits at original (i+0.5)/s-0.5
	return lattice{vx: float64(W) / tilesFrame * s, uy: float64(H) / tilesFrame * s, ox: off, oy: off}, ww, wh, s
}

// embedSync adds the sync pattern (blind, the same in every tile). Returns
// the correlation z measured at the known lattice.
func embedSync(wi *workImage, sl *syncLayout, strength float64, useMask bool) float64 {
	L, ww, wh, s := frameLattice(wi.W, wi.H)
	var mask []float64
	if useMask {
		mask = makeMaskWH(wi.resampleLuma(ww, wh), ww, wh)
		wi.computeGate()
		defer func() { wi.gate = nil }()
	}
	measure := func() syncRead {
		a := analyze(wi.resampleLuma(ww, wh), ww, wh, false)
		return readSync(fold(a, L), L, sl, 0)
	}
	// the mask keeps flat areas free; textured areas get more so that the
	// pattern keeps its mean strength (at most 3×)
	gain := 1.0
	if mask != nil {
		sum := 0.0
		for y := 0; y < wi.H; y += 4 {
			my := min(int(float64(y)*s), wh-1)
			for x := 0; x < wi.W; x += 4 {
				sum += mask[my*ww+min(int(float64(x)*s), ww-1)] * float64(wi.gate[y*wi.W+x]) / 255
			}
		}
		mean := sum / float64(((wi.H+3)/4)*((wi.W+3)/4))
		gain = math.Min(1/math.Max(mean, 1e-3), 3)
	}
	amp := syncAmp * strength * gain
	Z := make([]complex128, tileL*tileL)
	for i := range sl.k {
		c := sl.ph[i] * complex(amp/2, 0)
		Z[((sl.l[i]+tileL)%tileL)*tileL+(sl.k[i]+tileL)%tileL] += c
		Z[((-sl.l[i]+tileL)%tileL)*tileL+(-sl.k[i]+tileL)%tileL] += cmplx.Conj(c)
	}
	fft2(Z, tileL, tileL, true)
	pat := make([]float64, tileL*tileL)
	for i, v := range Z {
		pat[i] = real(v)
	}
	wi.applyTile(pat, float64(wi.W)/tilesFrame, float64(wi.H)/tilesFrame, mask, ww, wh, s)
	return measure().z
}

// applyTile adds the periodic pattern (tileL×tileL per px×py pixels),
// weighted by the coarse mask (analysis size) and the full-res gate.
func (wi *workImage) applyTile(pat []float64, px, py float64, mask []float64, ww, wh int, s float64) {
	parallel(wi.H, func(y int) {
		d := make([]float64, wi.W)
		ty := math.Mod(float64(y)/py, 1) * tileL
		fy := math.Floor(ty)
		ry := ty - fy
		iy := int(fy) % tileL
		iy1 := (iy + 1) % tileL
		my := math.Min(math.Max((float64(y)+0.5)*s-0.5, 0), float64(wh-1))
		my0 := int(my)
		my1 := min(my0+1, wh-1)
		rmy := my - float64(my0)
		for x := 0; x < wi.W; x++ {
			tx := math.Mod(float64(x)/px, 1) * tileL
			fx := math.Floor(tx)
			rx := tx - fx
			ix := int(fx) % tileL
			ix1 := (ix + 1) % tileL
			v := (1-rx)*(1-ry)*pat[iy*tileL+ix] + rx*(1-ry)*pat[iy*tileL+ix1] +
				(1-rx)*ry*pat[iy1*tileL+ix] + rx*ry*pat[iy1*tileL+ix1]
			if mask != nil {
				mx := math.Min(math.Max((float64(x)+0.5)*s-0.5, 0), float64(ww-1))
				mx0 := int(mx)
				mx1 := min(mx0+1, ww-1)
				rmx := mx - float64(mx0)
				v *= (1-rmx)*(1-rmy)*mask[my0*ww+mx0] + rmx*(1-rmy)*mask[my0*ww+mx1] +
					(1-rmx)*rmy*mask[my1*ww+mx0] + rmx*rmy*mask[my1*ww+mx1]
			}
			if wi.gate != nil {
				v *= float64(wi.gate[y*wi.W+x]) / 255
			}
			d[x] = v
		}
		wi.addRow(y, d)
	})
}

// ---- both parts ----

type layouts struct {
	v1   *layout
	sync *syncLayout
}

func newLayouts(key []byte, namespace string) layouts {
	return layouts{newLayout(key, namespace), newSyncLayout(key, namespace)}
}

// embedAll adds the sync pattern, then the id (informed embedding, which
// also compensates what the sync pattern adds to its band).
func embedAll(wi *workImage, ls layouts, id uint32, strength float64, mask bool) EmbedInfo {
	before := wi.lumaPlane()
	syncZ := embedSync(wi, ls.sync, strength, mask)
	info := embedWatermark(wi, ls.v1, id, strength, mask)
	rms := wi.lumaRMSDiff(before)
	info.DeltaRMS = round2(rms)
	info.PSNR = round2(math.Min(20*math.Log10(255/math.Max(rms, 1e-9)), 99))
	info.SyncConfidence = round2(syncZ)
	return info
}

// detectAny reads the id directly and, if that fails, via the sync pattern.
func detectAny(wi *workImage, ls layouts, verbose bool) Result {
	r := detectWatermark(wi, ls.v1, verbose)
	if r.Found {
		return r
	}
	if rs, ok := detectSynced(wi, ls, verbose); ok && (rs.Found || r.Status == "none") {
		return rs
	}
	return r
}
