package watermark

import (
	"crypto/sha256"
	"math"
	"math/rand/v2"
	"sort"
)

// Spread-spectrum watermark in the full-frame DCT of a fixed N×N luma grid.
//
// The image luma is resampled to N×N (independent of size and aspect
// ratio), so scaling the picture only changes the resampling, not the
// position of the carriers. The carriers are mid/low frequencies
// (radius rMin..rMax of N), which survive downscaling to ~300 px and
// JPEG compression. Every coefficient of the band belongs to one slot
// (payload bit, CRC bit or pilot) and carries a key-dependent sign;
// detection correlates the whitened coefficients with those signs.

const (
	N          = 512
	rMin       = 24.0
	rMax       = 160.0
	winR       = 4 // half window for the local spectral RMS
	K          = int(rMax) + winR + 1
	payloadLen = 32 // bits of the id
	crcLen     = 16 // CRC-16 over the id
	pilotLen   = 8  // always +1, decides "watermark present"
	dataSlots  = payloadLen + crcLen
	slots      = dataSlots + pilotLen
	baseAlpha  = 0.08 // target slot correlation (whitened units)
	zClip      = 3.0
	maxGain    = 3.0 // limit of the adaptive gain: rather unverified than visible

	thresholdFound   = 2.5 // presence z-score needed together with CRC and pilots
	thresholdPresent = 6.0 // presence alone: watermark there, id unreadable
	chaseBits        = 6   // least reliable bits tried flipped (up to two at a time)
)

type layout struct {
	pos  []int32 // u*K + v
	slot []uint8
	sign []float64
}

// newLayout spreads the band over the slots for a key. The namespace is part
// of the seed ("{namespace}/1"): marks of another namespace are not found.
func newLayout(key []byte, namespace string) *layout {
	seed := sha256.Sum256(append([]byte(namespace+"/1\x00"), key...))
	rng := rand.New(rand.NewChaCha8(seed))
	var pos []int32
	for u := 0; u < K; u++ {
		for v := 0; v < K; v++ {
			r := math.Hypot(float64(u), float64(v))
			if r >= rMin && r <= rMax {
				pos = append(pos, int32(u*K+v))
			}
		}
	}
	rng.Shuffle(len(pos), func(i, j int) { pos[i], pos[j] = pos[j], pos[i] })
	l := &layout{pos: pos, slot: make([]uint8, len(pos)), sign: make([]float64, len(pos))}
	for i := range pos {
		l.slot[i] = uint8(i % slots)
		if rng.Uint64()&1 == 1 {
			l.sign[i] = 1
		} else {
			l.sign[i] = -1
		}
	}
	return l
}

// crc16 is CRC-16/CCITT-FALSE over the four id bytes.
func crc16(id uint32) uint16 {
	c := uint16(0xFFFF)
	for _, b := range []uint8{uint8(id >> 24), uint8(id >> 16), uint8(id >> 8), uint8(id)} {
		c ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x1021
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// symbols maps the id to ±1 per slot.
func symbols(id uint32) [slots]float64 {
	var s [slots]float64
	crc := crc16(id)
	for i := 0; i < slots; i++ {
		bit := uint32(1)
		switch {
		case i < payloadLen:
			bit = id >> (payloadLen - 1 - i) & 1
		case i < dataSlots:
			bit = uint32(crc>>(crcLen-1-(i-payloadLen))) & 1
		}
		s[i] = float64(bit)*2 - 1
	}
	return s
}

// localRMS returns sqrt(mean(c²)) over a (2winR+1)² window per coefficient.
func localRMS(c []float64) []float64 {
	sat := make([]float64, (K+1)*(K+1))
	for u := 0; u < K; u++ {
		row := 0.0
		for v := 0; v < K; v++ {
			row += c[u*K+v] * c[u*K+v]
			sat[(u+1)*(K+1)+v+1] = sat[u*(K+1)+v+1] + row
		}
	}
	s := make([]float64, K*K)
	for u := 0; u < K; u++ {
		u0, u1 := max(u-winR, 0), min(u+winR+1, K)
		for v := 0; v < K; v++ {
			v0, v1 := max(v-winR, 0), min(v+winR+1, K)
			sum := sat[u1*(K+1)+v1] - sat[u0*(K+1)+v1] - sat[u1*(K+1)+v0] + sat[u0*(K+1)+v0]
			s[u*K+v] = math.Sqrt(sum/float64((u1-u0)*(v1-v0))) + 1e-9
		}
	}
	return s
}

// boxStd returns the local standard deviation of x (N×N) in a
// (2r+1)² window.
func boxStd(x []float64, r int) []float64 {
	sat := make([]float64, (N+1)*(N+1))
	sat2 := make([]float64, (N+1)*(N+1))
	for y := 0; y < N; y++ {
		a, b := 0.0, 0.0
		for i := 0; i < N; i++ {
			v := x[y*N+i]
			a += v
			b += v * v
			sat[(y+1)*(N+1)+i+1] = sat[y*(N+1)+i+1] + a
			sat2[(y+1)*(N+1)+i+1] = sat2[y*(N+1)+i+1] + b
		}
	}
	out := make([]float64, N*N)
	for y := 0; y < N; y++ {
		y0, y1 := max(y-r, 0), min(y+r+1, N)
		for i := 0; i < N; i++ {
			x0, x1 := max(i-r, 0), min(i+r+1, N)
			n := float64((y1 - y0) * (x1 - x0))
			s1 := sat[y1*(N+1)+x1] - sat[y0*(N+1)+x1] - sat[y1*(N+1)+x0] + sat[y0*(N+1)+x0]
			s2 := sat2[y1*(N+1)+x1] - sat2[y0*(N+1)+x1] - sat2[y1*(N+1)+x0] + sat2[y0*(N+1)+x0]
			out[y*N+i] = math.Sqrt(math.Max(s2/n-(s1/n)*(s1/n), 0))
		}
	}
	return out
}

// minFilter is a separable (2r+1)² erosion.
func minFilter(m []float64, r int) []float64 {
	tmp := make([]float64, N*N)
	out := make([]float64, N*N)
	for y := 0; y < N; y++ {
		for i := 0; i < N; i++ {
			v := math.Inf(1)
			for j := max(i-r, 0); j <= min(i+r, N-1); j++ {
				v = math.Min(v, m[y*N+j])
			}
			tmp[y*N+i] = v
		}
	}
	for y := 0; y < N; y++ {
		for i := 0; i < N; i++ {
			v := math.Inf(1)
			for j := max(y-r, 0); j <= min(y+r, N-1); j++ {
				v = math.Min(v, tmp[j*N+i])
			}
			out[y*N+i] = v
		}
	}
	return out
}

// makeMask returns a 0..1 weight per canonical pixel: ~0 in flat areas
// (sky, skin, UI backgrounds) and next to them, ~1 in texture. The
// erosion keeps the watermark away from the flat side of edges, where
// it would show as halos.
func makeMask(x []float64) []float64 {
	const c = 3.0 // luma std at which the weight is 0.5
	m := boxStd(x, 2)
	for i, v := range m {
		m[i] = v / (v + c)
	}
	return minFilter(m, 1)
}

// EmbedInfo describes an embedding (Watermarker.Embed).
type EmbedInfo struct {
	DeltaRMS   float64 `json:"delta_rms"` // luma change, 0..255 scale
	PSNR       float64 `json:"psnr_db"`
	Passes     int     `json:"passes"`
	Verified   bool    `json:"verified"`   // detector reads the id back
	Confidence float64 `json:"confidence"` // presence score right after embedding
	Texture    float64 `json:"texture"`    // share of textured pixels (0..1)
	// correlation with the sync pattern right after embedding
	SyncConfidence float64 `json:"sync_confidence"`
}

// slotMeans returns the mean whitened correlation per slot and the
// whitened host RMS (sigma).
// Coefficients with u > uMax or v > vMax are skipped (lost by downscaling).
func slotMeans(c []float64, l *layout, uMax, vMax int) (mean [slots]float64, n [slots]int, sigma float64) {
	s := localRMS(c)
	sq := 0.0
	cnt := 0
	for i, p := range l.pos {
		if int(p)/K > uMax || int(p)%K > vMax {
			continue
		}
		cnt++
		v := math.Max(math.Min(c[p]/s[p], zClip), -zClip)
		sq += v * v
		mean[l.slot[i]] += v * l.sign[i]
		n[l.slot[i]]++
	}
	for k := range mean {
		mean[k] /= float64(max(n[k], 1))
	}
	return mean, n, math.Sqrt(sq/float64(max(cnt, 1))) + 1e-12
}

// embedWatermark uses informed embedding: per slot only the difference
// between the host's own correlation and the target is added, so the
// host image does not interfere with detection and a small change is
// enough. The result is measured on the real image and corrected in up
// to five passes; the gain adapts to what the mask, the resampling and
// the rounding let through. Finally the detector checks the result.
func embedWatermark(wi *workImage, l *layout, id uint32, strength float64, mask bool) EmbedInfo {
	before := wi.lumaPlane()
	x0 := wi.canonical()
	sym := symbols(id)
	target := baseAlpha * strength
	var m []float64
	if mask {
		m = makeMask(x0)
		wi.computeGate()
		defer func() { wi.gate = nil }()
	}
	texture := 1.0
	if wi.gate != nil {
		sum := 0
		for _, g := range wi.gate {
			sum += int(g)
		}
		texture = float64(sum) / 255 / float64(len(wi.gate))
	}
	gain := 1.2
	var prevMean, prevReq [slots]float64
	passes := 0
	x := x0
	for pass := 0; pass < 5; pass++ {
		c := dctForward(x, K)
		mean, _, _ := slotMeans(c, l, K, K)
		if pass > 0 {
			got, asked := 0.0, 0.0
			for k := range mean {
				if prevReq[k] != 0 {
					got += (mean[k] - prevMean[k]) * sym[k]
					asked += prevReq[k]
				}
			}
			if asked > 0 && got > 0 {
				gain = math.Min(math.Max(gain*asked/got, 1), maxGain)
			}
		}
		var need [slots]float64
		worst := 0.0
		for k := range need {
			prevReq[k] = 0
			if g := target - sym[k]*mean[k]; g > 0 {
				prevReq[k] = g
				need[k] = g * sym[k] * gain
				worst = math.Max(worst, g)
			}
		}
		prevMean = mean
		if worst < 0.15*target {
			break
		}
		s := localRMS(c)
		dc := make([]float64, K*K)
		for i, p := range l.pos {
			dc[p] = need[l.slot[i]] * l.sign[i] * s[p]
		}
		d := dctInverse(dc, K)
		for i := range m {
			d[i] *= m[i]
		}
		wi.applyDelta(d)
		passes++
		x = wi.canonical()
	}
	rms := wi.lumaRMSDiff(before)
	before = nil
	r := detectWatermark(wi, l, false)
	return EmbedInfo{
		DeltaRMS:   round2(rms),
		PSNR:       round2(math.Min(20*math.Log10(255/math.Max(rms, 1e-9)), 99)),
		Passes:     passes,
		Verified:   r.Found && *r.ID == id,
		Texture:    round2(texture),
		Confidence: r.Confidence,
	}
}

var orientationNames = [8]string{
	"none", "mirrored horizontally", "mirrored vertically", "rotated 180°",
	"transposed", "rotated 90° clockwise", "rotated 90° counter-clockwise", "transverse",
}

// orient returns the candidate original for a suspect that underwent
// orientation o (see orientationNames).
func orient(x []float64, o int) []float64 {
	if o == 0 {
		return x
	}
	out := make([]float64, N*N)
	for y := 0; y < N; y++ {
		for i := 0; i < N; i++ {
			sy, sx := y, i
			if o&4 != 0 {
				sy, sx = sx, sy
			}
			if o&1 != 0 {
				sx = N - 1 - sx
			}
			if o&2 != 0 {
				sy = N - 1 - sy
			}
			out[y*N+i] = x[sy*N+sx]
		}
	}
	return out
}

// Result of a detection (Watermarker.Detect).
type Result struct {
	Status      string    `json:"status"` // found | present_unreadable | none
	Found       bool      `json:"found"`
	ID          *uint32   `json:"id,omitempty"`
	Confidence  float64   `json:"confidence"` // presence z-score
	CRCOK       bool      `json:"crc_ok"`
	Corrected   int       `json:"corrected_bits"`
	MinBitZ     float64   `json:"min_bit_z"`
	Orientation string    `json:"orientation"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	BitZ        []float64 `json:"bit_z,omitempty"`
	// read via the sync pattern (image was cropped, rotated or pasted)
	Synced         bool    `json:"synced,omitempty"`
	Rotation       float64 `json:"rotation_deg,omitempty"` // clockwise, if not a multiple of 90°
	SyncConfidence float64 `json:"sync_confidence,omitempty"`
}

func slotScores(x []float64, l *layout, uMax, vMax int) (z [slots]float64) {
	mean, n, sigma := slotMeans(dctForward(x, K), l, uMax, vMax)
	for k := range z {
		z[k] = mean[k] * math.Sqrt(float64(n[k])) / sigma
	}
	return
}

// presence combines all slots: pilots must be positive, data slots must
// be far from zero. Under "no watermark" every slot score is ~N(0,1), so
// the statistic is ~N(0,1) as well.
func presence(z [slots]float64) float64 {
	const absMean, absVar = 0.7979, 0.3634 // E|N(0,1)|, Var|N(0,1)|
	t := 0.0
	for k := 0; k < dataSlots; k++ {
		t += math.Abs(z[k]) - absMean
	}
	for k := dataSlots; k < slots; k++ {
		t += z[k]
	}
	return t / math.Sqrt(dataSlots*absVar+pilotLen)
}

// detectWatermark tries all eight orientations. An id counts as found
// when the CRC-16 matches (after at most two soft-corrected bits), all
// pilots are positive and the presence statistic reaches thresholdFound.
// The false-positive rate for an unmarked image is about 1e-6.
func detectWatermark(wi *workImage, l *layout, verbose bool) Result {
	x := wi.canonical()
	type cand struct {
		z     [slots]float64
		p     float64
		id    uint32
		flips int
		ok    bool
	}
	var cands [8]cand
	parallel(8, func(o int) {
		// a coefficient survives downscaling if its frequency stays below
		// ~1/4 cycle per pixel of the suspect image
		uMax, vMax := wi.H*55/100, wi.W*55/100
		if o&4 != 0 {
			uMax, vMax = vMax, uMax
		}
		z := slotScores(orient(x, o), l, uMax, vMax)
		c := cand{z: z, p: presence(z)}
		c.id, c.flips, c.ok = decodeBits(z[:])
		for k := dataSlots; k < slots; k++ {
			if z[k] <= 0 {
				c.ok = false
			}
		}
		c.ok = c.ok && c.p >= thresholdFound
		cands[o] = c
	})
	bestO := -1
	for o, c := range cands {
		if c.ok && (bestO < 0 || c.p > cands[bestO].p) {
			bestO = o
		}
	}
	if bestO < 0 {
		bestO = 0
		for o, c := range cands {
			if c.p > cands[bestO].p {
				bestO = o
			}
		}
	}
	best := cands[bestO]
	minZ := math.Inf(1)
	for k := 0; k < dataSlots; k++ {
		minZ = math.Min(minZ, math.Abs(best.z[k]))
	}
	r := Result{
		Confidence:  round2(best.p),
		CRCOK:       best.ok,
		Corrected:   best.flips,
		MinBitZ:     round2(minZ),
		Orientation: orientationNames[bestO],
		Width:       wi.W,
		Height:      wi.H,
	}
	switch {
	case best.ok:
		r.Status = "found"
		r.ID = &best.id
	case best.p >= thresholdPresent:
		r.Status = "present_unreadable"
	default:
		r.Status = "none"
	}
	r.Found = best.ok
	if verbose {
		for _, v := range best.z {
			r.BitZ = append(r.BitZ, round2(v))
		}
	}
	return r
}

// round2 rounds to two decimals; NaN and ±Inf (flat images) become 0, as JSON
// cannot carry them.
func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

// decodeBits reads id and CRC from the slot scores. If the CRC fails, the
// least reliable bits are flipped (one or two at a time): soft decoding
// that rescues images where a few bits drowned in noise.
func decodeBits(z []float64) (id uint32, flips int, ok bool) {
	var bits [dataSlots]bool
	for k := range bits {
		bits[k] = z[k] > 0
	}
	check := func() (uint32, bool) {
		var v uint32
		var c uint16
		for k := 0; k < dataSlots; k++ {
			b := uint32(0)
			if bits[k] {
				b = 1
			}
			if k < payloadLen {
				v = v<<1 | b
			} else {
				c = c<<1 | uint16(b)
			}
		}
		return v, crc16(v) == c
	}
	if v, good := check(); good {
		return v, 0, true
	}
	order := make([]int, dataSlots)
	for k := range order {
		order[k] = k
	}
	sort.Slice(order, func(i, j int) bool { return math.Abs(z[order[i]]) < math.Abs(z[order[j]]) })
	weak := order[:chaseBits]
	for _, a := range weak {
		bits[a] = !bits[a]
		if v, good := check(); good {
			return v, 1, true
		}
		bits[a] = !bits[a]
	}
	for i, a := range weak {
		for _, b := range weak[i+1:] {
			bits[a], bits[b] = !bits[a], !bits[b]
			if v, good := check(); good {
				return v, 2, true
			}
			bits[a], bits[b] = !bits[a], !bits[b]
		}
	}
	return 0, 0, false
}
