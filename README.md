# image-watermark

[Deutsche Fassung](README.de.md)

Invisible frequency-domain watermark for images. It survives scaling, JPEG compression,
distorted scaling, rotation by 90°/180°, mirroring, slight blurring and – thanks to a sync
pattern – also cropping, rotation by any angle and pasting into a collage. A single,
statically linked program without dependencies (Linux x86-64/ARM64, macOS, Windows, ~6 MB)
embeds, detects and, if needed, serves the checking website itself.

Download: [Releases](https://github.com/andreasmayercode/image-watermark/releases) –
one archive per platform with the program, the checking website (`web/`), license and
checksums (`SHA256SUMS`). The method is also available as a Go library, and applications
can build their own program on it (see “Go library”).

```
image-watermark embed  -in photo.jpg -out photo-wm.jpg -id 4711 -key-file watermark.key
image-watermark detect -in suspect.jpg -key-file watermark.key
image-watermark serve  -listen 127.0.0.1:8080 -key-file watermark.key
```

## Method (brief)

1. The image's brightness (luma) is resampled to a fixed 512 × 512 grid, regardless of
   size and aspect ratio. Scaling therefore only changes the resampling, not the position
   of the watermark.
2. The two-dimensional DCT is computed on this grid. The watermark lies in a ring of low to
   medium frequencies (12–80 cycles per image width). These frequencies survive
   downscaling and JPEG.
3. The ~19,600 coefficients of the ring are distributed to 56 slots depending on the key:
   32 bits of ID, 16 bits of CRC, 8 pilot bits. Each coefficient gets a key-dependent sign
   (spread spectrum).
4. **Informed embedding:** For each slot, only the difference between what the image
   already “brings along” and the target value is added. The image itself hardly
   interferes with detection, and a small change is enough. The result is measured again
   on the actual image and readjusted in up to five passes.
5. **Perceptual mask:** The change goes into textured image areas (foliage, fabric, snow,
   edges). Flat areas (sky, skin, white background) remain largely unchanged down to the
   pixel.
6. **Detection:** the same resampling and correlation in all eight orientations (rotated,
   mirrored). An ID counts as found when CRC-16 and all eight pilot bits match and the
   overall signal reaches the threshold. Up to two uncertain bits are corrected by soft
   decoding.

After embedding, the program reads the ID back itself (`verified`) and warns if that does
not succeed reliably.

### Sync pattern

The 512 grid is tied to the image frame. If someone crops off a border, rotates the image
by an arbitrary angle or puts it into a collage, all carriers shift – without help the ID
can no longer be read. For this case the program additionally places a faint
**sync pattern** in the image:

1. The image is divided into 8 × 8 tiles (width/8 × height/8 each). Every tile gets the
   same key-dependent pattern of 40 sinusoids with 10–28 cycles per tile – above the band
   of the ID, so the two do not interfere. The pattern is embedded first; the informed
   embedding of the ID then compensates for it.
2. **Detection:** If `detect` cannot read the ID directly, it searches for the pattern. In
   the spectrally whitened image the repetition creates a lattice of peaks; the
   autocorrelation yields the two lattice vectors (rotation, scale, distortion). Spectral
   lines (image edges, collage borders) are suppressed beforehand. In addition, for
   axis-parallel lattices (cropping without rotation) the search lays a “comb” over all
   tile sizes that a crop of up to 40 % per axis allows and sums the spectral energy
   exactly at the 40 known pattern frequencies. This finds the lattice even when the
   image's own regular structures (lines of text, UI elements) mask the autocorrelation.
3. The image is folded along this lattice into a single tile. A cyclic correlation with
   the known pattern (FFT, eight orientations) yields phase and orientation; the lattice
   is then refined for maximum correlation. A lattice counts as found from z ≥ 7.
4. The local correlation per tile shows which tiles carry the pattern; the best 8 × 8
   windows are candidates for the original frame. For each, the 512 grid is sampled
   directly from the suspect image (one tile = 64 grid points), cropped-off parts are
   continued smoothly from the border, then the normal decoder reads it (CRC, pilot bits,
   signal strength).
5. For collages, sub-windows (2/3 and 1/2 of the image size) are searched as well, because
   the pasted image takes up a larger share there.

The result then carries `"synced": true`, the strength of the pattern (`sync_confidence`)
and, for arbitrary angles, `rotation_deg` (clockwise). Cost: about 0.5 dB PSNR. A check
without a match takes longer (1–3 s) because all windows are searched.

## Measured robustness

41 photos (1200–2240 px wide, including panoramas and cut-out products), ID 3141592653,
default strength, version 1.0.0. “OK” = ID read correctly; in no case was a wrong ID read.
Two images with very little texture (`verified: false` already when embedding) are
included in the count.

| Change to the marked image | read |
|---|---|
| unchanged (JPEG 85), JPEG 50 | 39/41 |
| downscaled to 25 % / to 400 px width | 38/41, 39/41 |
| to 300 px width | 35/41 |
| distorted scaling (70 % × 50 %), rotated 90° | 40/41 |
| blur (to ⅓ and back) | 39/41 |
| **2 % border cropped** | **37/41** |
| **unevenly cropped (10/3/17/8 %, ~26 % of the area)** | **31/41** |
| **rotated by 3°** (canvas enlarged) | **37/41** |
| rotated by 17° and cropped to the interior | 17/41 |
| rotated by −33° | 21/41 |
| rotated by 45° and downscaled to 50 % | 20/41 |
| **pasted into a collage, 60 % of the width** | **28/41** |
| pasted into a collage, 40 % of the width | 12/41 |
| cropped (~30 % of the area) and downscaled to 50 % | 15/41 |
| center crop 50 % × 50 %, collage at 25 % of the width | 0/41 |

Second sample: 54 camera photos up to 16 MP and 6 screenshots (PNG).

| Change | 54 photos | 6 screenshots |
|---|---|---|
| unchanged (JPEG 85) | 48/54 | 5/6 |
| 2 % border cropped | 48/54 | 3/6 |
| unevenly cropped (~26 % of the area) | 30/54 | 2/6 |
| rotated by 3° / −33° | 46/54, 45/54 | 2/6, 2/6 |
| rotated by 45° and downscaled to 50 % | 46/54 | 2/6 |
| rotated by 17° and cropped to the interior | 4/54 | 1/6 |
| collage at 60 % / 40 % of the width | 37/54, 23/54 | 2/6, 1/6 |

The 6 unreadable photos are very soft subjects (haze, backlight, sunset): there, embedding
the ID already fails (`verified: false`), and the sync pattern alone
does not help. Screenshots with a lot of white and text carry little watermark; an almost
empty window (texture 0.05) takes none at all. False positives: all 60 images unchanged,
cropped and rotated with a different key – no match (highest signal 3.2).

Rule of thumb: a border of a few percent, a slight rotation and pasting as a large part of
a collage are no problem. The more area is missing, the less reliable it gets: the ID is
embedded in an informed way (it compensates for the image itself) and therefore needs most
of the original frame. With larger losses, `detect` often reports at least
`present_unreadable`.

False positives: 301 unmarked images, not a single match (highest signal 3.4; in
addition, CRC-16 and eight pilot bits must match). With sync search: 41 unmarked
images each unchanged, cropped and rotated, no match (highest signal 3.3). With a wrong
key nothing is detected.

Visibility: For photos no difference is visible at 1:1 (luma change RMS 3–4, PSNR 36–38 dB,
concentrated in textured areas). **For graphics and screenshots** (large flat areas, text)
the watermark is visible as slight streaks along text and edges. The program then warns
(“large flat areas”).

### Small images

Embedded directly into 640 × 400 px (five subjects):

| Change | Result |
|---|---|
| unchanged, JPEG 60, downscaled to 75 %, 400 px wide | 5/5 |
| to 50 % (320 × 200) | 5/5 |
| to 256 px width | 3/5 |
| distorted scaling, rotated 90°, blurred, enlarged to 200 % | 5/5 |

Rule of thumb: readable as long as the image stays about **300 px wide or 200 px on the
short side**. Below that it becomes unreliable; embedded directly into 256 × 160, `embed`
sometimes reports `verified: false`.

### Runtime and memory

Measured with 1.0.0 on 8 cores (ARM64):

| Image | Embed | Detect directly | Detect via sync (10 % cropped) | unmarked (all windows) | Peak memory |
|---|---|---|---|---|---|
| 640 × 400 | 0.3 s | 0.13 s | 0.45 s | 1.1 s | 60 MB |
| 6000 × 4000 (24 MP) | 2.3 s | 0.4 s | 0.85 s | 2.6 s | 240 MB |

The program runs as a separate process. PHP's `memory_limit` does not apply to it, but the
host's limits per process or per account (e.g. `ulimit`) do. If needed, the environment
variable `GOMEMLIMIT=300MiB` limits memory.

## Limitations

- **Heavy cropping** (such as a center crop with half the width and height) and small
  collage shares (below about 40 % of the width) are not survived by the ID, nor in many
  cases is strong rotation with cropping. The sync pattern is then often still found
  (`present_unreadable`), but the ID needs most of the original frame. Very wide formats
  (panoramas, banners) have flat tiles and are less robust.
- Below about 300 px width / 200 px short side the ID becomes unreliable. For thumbnails
  with a clearly different crop (e.g. square from landscape), keep embedding the watermark
  again after cropping.
- Anyone who knows the key and the method can deliberately remove the watermark. The key
  must stay secret. A watermark is an indication of origin, not cryptographic proof.
- With a new key, old watermarks can no longer be read. So never throw a key away.
  Several keys = several checks.
- 16-bit PNGs are saved as 8 bit. CMYK JPEGs are converted to RGB (the CMYK color profile
  is dropped). Output only as JPEG or PNG.

## Installation (Linux)

```bash
tar -xzf image-watermark-1.0.0-linux-amd64.tar.gz
mkdir -p ~/watermark/bin ~/watermark/private
cp image-watermark-1.0.0-linux-amd64/image-watermark ~/watermark/bin/image-watermark
chmod 755 ~/watermark/bin/image-watermark
head -c 32 /dev/urandom | base64 > ~/watermark/private/watermark.key
chmod 600 ~/watermark/private/watermark.key
~/watermark/bin/image-watermark version
```

The key is passed via `-key-file` (recommended), the environment variable
`IMAGE_WM_KEY` or `-key` (then visible in the process list).

### Integrating into image processing

```bash
image-watermark embed -in upload.jpg -out upload.jpg -id 4711 \
    -key-file ~/watermark/private/watermark.key -json
```

- Embed the watermark into the image that is **delivered**, or into the largest one.
  Smaller versions derived from it, from ~400 px on, keep it.
- `-id` stores a number from 0 to 4,294,967,295 (32 bits) – for example a customer, site
  or photographer number from your own records. More bits would make every bit more
  error-prone (not more compute-intensive). Programs built on the package `cli` can show
  the number as text, e.g. a short code (see “Own programs”).
- `-in`/`-out` may be the same file (replaced atomically) or `-` for stdin/stdout. Input
  JPEG, PNG, GIF, WebP; output according to the extension `.jpg`/`.png`.
- EXIF, ICC profile, XMP and IPTC are preserved for JPEG.
- `-strength 1.5` makes it more robust (and more likely visible), `-quality 92` is the
  JPEG quality, `-no-mask` distributes evenly (only for special cases).
- `-namespace my-app` embeds in a namespace of your own (letters, digits, `.`, `_`, `-`;
  default `image-watermark`). Marks of one namespace are not found with another one, so
  `detect` and `serve` need the same `-namespace`. This lets an application use the
  ready-made binary instead of building its own program (see “Go library”).
- Exit code 0 = embedded. With `verified: false` (image has too little texture) a warning
  is written to stderr, likewise with a weak sync pattern (`sync_confidence` below 7:
  cropping and rotation then probably not readable).

JSON output of `embed -json`:

```json
{"ok":true,"id":4711,"width":1920,"height":1280,"verified":true,"confidence":8.53,
 "texture":0.51,"delta_rms":3.18,"psnr_db":38.08,"sync_confidence":24.1}
```

### Detection

```bash
image-watermark detect -in image.jpg -key-file … [-json] [-v]
```

Exit code 0 = found, 1 = not found, 2 = error.

```json
{"status":"found","found":true,"id":4711,"confidence":8.99,"crc_ok":true,
 "corrected_bits":0,"min_bit_z":1.35,"orientation":"none","width":960,"height":640}
```

Read via the sync pattern (here: image rotated by 12°):

```json
{"status":"found","found":true,"id":4711,"confidence":8.42,"crc_ok":true,
 "corrected_bits":0,"min_bit_z":1.01,"orientation":"none","width":2146,"height":1654,
 "synced":true,"rotation_deg":12.01,"sync_confidence":24.29}
```

`orientation` is always one of the
eight names (nearest multiple of 90°); `rotation_deg` is added for arbitrary angles.
`status`: `found`, `present_unreadable` (clear signal, ID not readable) or `none`.
`confidence` is the signal strength (unmarked images are mostly below 3, marked ones
between 5 and 10).

## Checking website

`web/index.html` is the upload page (German/English depending on the browser language,
light/dark). It sends the image to `detect.php` in the same directory. Images are not
stored.

**Option A: PHP web hosting.** Copy `index.html` and `detect.php` into a directory of the
website, binary and key **outside** the document root. The paths are at the top of
`detect.php` (default: `../bin/image-watermark`, `../private/watermark.key`) or come from
the environment variables `IMAGE_WM_BINARY` and `IMAGE_WM_KEY_FILE` (Apache: `SetEnv`);
`IMAGE_WM_NAMESPACE` sets `-namespace` if the images were marked with one.
PHP needs `proc_open` (disabled on some shared hosting plans, then option B).
`upload_max_filesize` and `post_max_size` must match the image size.

**Option B: built-in server** (VPS/root server). The binary serves the page and the
endpoint itself:

```bash
image-watermark serve -listen 127.0.0.1:8080 -key-file … -max-mb 30
```

In front of it a reverse proxy with TLS, e.g. Apache:

```apache
ProxyPass        /watermark/ http://127.0.0.1:8080/
ProxyPassReverse /watermark/ http://127.0.0.1:8080/
```

systemd unit (`/etc/systemd/system/image-watermark.service`):

```ini
[Unit]
Description=image-watermark detection website
After=network.target

[Service]
ExecStart=/opt/watermark/bin/image-watermark serve -listen 127.0.0.1:8080 -key-file /opt/watermark/private/watermark.key
User=www-data
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

The server processes at most two images at a time, rejects images over 120 megapixels and
sets a restrictive Content Security Policy. There is no rate limiting per IP; if needed,
that belongs in the proxy.

## Go library

```go
import watermark "github.com/andreasmayercode/image-watermark"

w, err := watermark.New(key, watermark.Options{Namespace: "my-app"})
src, err := watermark.Load("photo.jpg")
marked, info := w.Embed(src.Image, 4711, watermark.EmbedOptions{})
err = watermark.Save("photo-wm.jpg", marked, "jpeg", 92, src.Meta)

r := w.Detect(img, false) // r.Found, *r.ID, r.Confidence, r.Synced …
```

- `New` takes the secret key (at least 8 bytes) and a **namespace**. The namespace is part
  of the data format: marks of one namespace are not found with another one, even with the
  same key. Choose it once per application and never change it. Empty = `image-watermark`.
- `Embed` changes images of type `*image.YCbCr`, `*image.Gray` and `*image.NRGBA` in
  place; `EmbedInfo.Verified = false` means: too little texture, the ID may not be read
  back reliably.
- `Load`/`Decode` accept JPEG, PNG, GIF and WebP (64 × 64 px up to 120 megapixels) and keep
  the JPEG metadata in `Source.Meta`; `Save`/`Encode` write JPEG or PNG.
- A `Watermarker` may be used by several goroutines at the same time.

### Own programs

The package `cli` is the complete command line program (embed, detect, serve, version).
An application builds its own program with its name, key variable, namespace, checking
page and – optionally – a `cli.Codec` that shows the 32-bit ID as text (flag and JSON
field of the codec's name, e.g. `-code AM-NRD` / `"code":"AM-NRD"`):

```go
package main

import "github.com/andreasmayercode/image-watermark/cli"

func main() {
	cli.Main(cli.Config{
		Name: "my-watermark", Version: "1.0.0", KeyEnv: "MY_WM_KEY",
		Namespace: "my-app", Codec: myCodec{},
	})
}
```

## Building

No local Go installation is needed. The build runs in the official Go container:

```bash
./build.sh          # dist/image-watermark-linux-amd64
./build.sh all      # additionally linux-arm64 and darwin-arm64
```

With Go installed (version according to `go.mod`),
`go install github.com/andreasmayercode/image-watermark/cmd/image-watermark@latest` works as well.

Releases are built by GitHub Actions: a tag `vX.Y.Z` that matches the version in
`cmd/image-watermark/main.go`
creates the archives for Linux (amd64, arm64), macOS (arm64) and Windows (amd64) and
attaches them to a GitHub release.

Dependency: `golang.org/x/image` (WebP decoder only). The data format of the ID carries
`{namespace}/1`, the sync pattern `{namespace}/sync1` (namespace `image-watermark` unless a
program sets its own, see “Go library”). Anyone who changes the namespace, grid, frequency
ring, bit allocation or tile pattern makes existing watermarks unreadable.

Measuring robustness (photos in a directory, takes a few minutes):

```bash
WM_IMAGES=/path/to/photos WM_LIMIT=10 go test -run 'TestAttacks|TestFalsePositives' -v -timeout 60m
```

## Disclaimer

image-watermark is provided without any warranty, to the extent permitted by law (see
sections 15 and 16 of the `LICENSE`). This applies to the source code as well as to the
ready-made programs (binaries):

- The binaries are built automatically by GitHub Actions from the tagged source code. They
  are not signed, so macOS and Windows may show a warning on first launch. Verify
  downloaded archives with `SHA256SUMS`, or build the program yourself from source (see
  “Building”).
- You run the program at your own risk. Test it with copies first. If `-in` and `-out` are
  the same file, the original is replaced – keep originals separately.
- Whether your hosting contract allows running your own programs, and which limits apply
  to memory and CPU time, is solely up to your provider. Check this before installing. The
  examples in this guide are not a recommendation of any particular provider or plan.
- A detected watermark is an indication of origin, not proof; a watermark that is not
  detected does not prove that an image was not marked (see “Limitations”). The results
  are not legal advice.

## License

Copyright (C) 2026 the image-watermark authors. image-watermark is free software under the GNU
Affero General Public License, version 3 or later (`LICENSE`). Anyone who runs a modified
version as a network service (e.g. the checking website via `serve`) must offer its source
code to the users – the page links to this repository for that purpose.

The binaries contain the Go standard library and `golang.org/x/image` (BSD-3-Clause), see
`THIRD-PARTY-NOTICES.md`.

## Third-party rights

If you believe that image-watermark or its use infringes your rights, please contact us
first via the [issues](https://github.com/andreasmayercode/image-watermark/issues) of this
repository. I will look into it promptly and change or remove the
affected part where necessary.
