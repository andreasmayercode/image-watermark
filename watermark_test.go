package watermark

import (
	"bytes"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

// texture is a deterministic photo-like test image: smooth gradients plus noise.
func texture(w, h int) *image.NRGBA {
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 60 + (x*90)/w + (y*60)/h + rng.IntN(50)
			img.Set(x, y, color.NRGBA{uint8(v), uint8(v * 9 / 10), uint8(v * 8 / 10), 255})
		}
	}
	return img
}

func TestRoundTrip(t *testing.T) {
	key := []byte("test-key-0123456789")
	w, err := New(key, Options{})
	if err != nil {
		t.Fatal(err)
	}
	marked, info := w.Embed(texture(640, 480), 4711, EmbedOptions{})
	if !info.Verified {
		t.Fatalf("not verified: %+v", info)
	}
	// through JPEG
	var buf bytes.Buffer
	if err := Encode(&buf, marked, "jpeg", 85, nil); err != nil {
		t.Fatal(err)
	}
	src, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r := w.Detect(src.Image, false)
	if !r.Found || r.ID == nil || *r.ID != 4711 {
		t.Fatalf("not found: %+v", r)
	}

	// another namespace or key does not read it
	other, _ := New(key, Options{Namespace: "other-app"})
	if r := other.Detect(src.Image, false); r.Found {
		t.Errorf("found with another namespace: %+v", r)
	}
	otherKey, _ := New([]byte("another-key-987654321"), Options{})
	if r := otherKey.Detect(src.Image, false); r.Found {
		t.Errorf("found with another key: %+v", r)
	}
}

func TestNewChecks(t *testing.T) {
	if _, err := New([]byte("short"), Options{}); err == nil {
		t.Error("short key accepted")
	}
	if _, err := New([]byte("long-enough-key"), Options{Namespace: "a/b"}); err == nil {
		t.Error("namespace with / accepted")
	}
}

func TestFlatImageFinite(t *testing.T) {
	w, _ := New([]byte("test-key-0123456789"), Options{})
	img := image.NewNRGBA(image.Rect(0, 0, 200, 200))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	_, info := w.Embed(img, 1, EmbedOptions{})
	for _, v := range []float64{info.Confidence, info.SyncConfidence, info.PSNR, info.DeltaRMS, info.Texture} {
		if v != v || v > 1e300 || v < -1e300 {
			t.Fatalf("non-finite value in %+v", info)
		}
	}
}
