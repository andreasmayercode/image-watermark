// image-watermark: invisible frequency-domain watermark for images that
// survives scaling, JPEG compression, cropping, rotation and pasting into a
// collage.
//
// Copyright (C) 2026 the image-watermark authors (https://github.com/andreasmayercode/image-watermark)
// SPDX-License-Identifier: AGPL-3.0-or-later
// See LICENSE and THIRD-PARTY-NOTICES.md.
package main

import "github.com/andreasmayercode/image-watermark/cli"

var version = "1.0.0"

func main() {
	cli.Main(cli.Config{
		Name:    "image-watermark",
		Version: version,
		KeyEnv:  "IMAGE_WM_KEY",
	})
}
