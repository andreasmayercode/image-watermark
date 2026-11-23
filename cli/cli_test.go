package cli

import (
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// TestNamespaceFlag: a mark embedded with -namespace is found only with the
// same namespace.
func TestNamespaceFlag(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.png"), filepath.Join(dir, "out.png")
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			v := uint8(60 + x*90/640 + y*60/480 + rng.IntN(50))
			img.Set(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	f, err := os.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg := Config{Name: "test", Version: "0", KeyEnv: "TEST_WM_KEY"}
	key := []string{"-key", "test-key-0123456789"}
	run := func(args ...string) int { return Run(cfg, append(args, key...)) }

	if c := run("embed", "-in", in, "-out", out, "-id", "4711", "-namespace", "other-app"); c != 0 {
		t.Fatalf("embed: exit %d", c)
	}
	if c := run("detect", "-in", out, "-namespace", "other-app"); c != 0 {
		t.Fatalf("detect with the same namespace: exit %d, want 0", c)
	}
	if c := run("detect", "-in", out); c != 1 {
		t.Fatalf("detect with the default namespace: exit %d, want 1", c)
	}
	if c := run("detect", "-in", out, "-namespace", "bad/ns"); c != 2 {
		t.Fatalf("invalid namespace: exit %d, want 2", c)
	}
}
