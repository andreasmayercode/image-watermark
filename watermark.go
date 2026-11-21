// Package watermark embeds and detects an invisible, robust watermark in
// images: a 32-bit number in the frequency domain of the image brightness. It
// survives scaling, JPEG compression, distorted scaling, rotation by 90°/180°,
// mirroring, slight blurring and – thanks to a sync pattern – also cropping,
// rotation by any angle and pasting into a collage.
//
// Embedding and detection need the same secret key and the same namespace.
// The namespace separates the marks of different applications that might use
// the same key; it is part of the data format and must never change once
// images have been marked.
//
//	w, err := watermark.New(key, watermark.Options{})
//	src, err := watermark.Load("photo.jpg")
//	marked, info := w.Embed(src.Image, 4711, watermark.EmbedOptions{})
//	err = watermark.Save("photo-wm.jpg", marked, "jpeg", 92, src.Meta)
//	r := w.Detect(marked, false) // r.Found, *r.ID == 4711
//
// Copyright (C) 2026 the image-watermark authors (https://github.com/andreasmayercode/image-watermark)
// SPDX-License-Identifier: AGPL-3.0-or-later
// See LICENSE and THIRD-PARTY-NOTICES.md.
package watermark

import (
	"errors"
	"image"
	"strings"
)

// DefaultNamespace is used when Options.Namespace is empty.
const DefaultNamespace = "image-watermark"

// MinKeyLength is the shortest secret key accepted (bytes).
const MinKeyLength = 8

// SyncThreshold: below this EmbedInfo.SyncConfidence the sync pattern is weak –
// the id may not be readable after cropping or rotation.
const SyncThreshold = syncThreshold

// LowTexture: below this EmbedInfo.Texture the image has large flat areas
// (graphic, screenshot) where the watermark may be visible.
const LowTexture = 0.22

// Options configure a Watermarker.
type Options struct {
	// Namespace separates the marks of different applications (letters,
	// digits, ".", "_" and "-"). Empty = DefaultNamespace.
	Namespace string
}

// EmbedOptions tune a single embedding.
type EmbedOptions struct {
	Strength float64 // 1.0 (also for 0) = default; higher = more robust, more visible (0.1 … 5)
	NoMask   bool    // spread the watermark evenly (no perceptual mask)
}

// Watermarker embeds and detects with one key and namespace. It is safe for
// concurrent use.
type Watermarker struct {
	ls layouts
}

// New prepares the carrier layouts for a key (at least MinKeyLength bytes).
func New(key []byte, opt Options) (*Watermarker, error) {
	if len(key) < MinKeyLength {
		return nil, errors.New("secret key missing or shorter than 8 characters")
	}
	ns := opt.Namespace
	if ns == "" {
		ns = DefaultNamespace
	}
	if strings.Trim(ns, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "" {
		return nil, errors.New("namespace may only contain letters, digits, \".\", \"_\" and \"-\"")
	}
	return &Watermarker{ls: newLayouts(key, ns)}, nil
}

// Embed hides id in img and returns the marked image (img itself if it is a
// *image.YCbCr, *image.Gray or *image.NRGBA – it is changed in place) and
// measurements. EmbedInfo.Verified = false: the image has too little texture,
// the id may not be read back reliably.
func (w *Watermarker) Embed(img image.Image, id uint32, opt EmbedOptions) (image.Image, EmbedInfo) {
	strength := opt.Strength
	if strength <= 0 {
		strength = 1
	}
	wi := newWorkImage(img)
	info := embedAll(wi, w.ls, id, strength, !opt.NoMask)
	return wi.img, info
}

// Detect reads the id directly and, if that fails, via the sync pattern
// (cropped, rotated or pasted images; Result.Synced). verbose adds the score
// of every bit (Result.BitZ).
func (w *Watermarker) Detect(img image.Image, verbose bool) Result {
	return detectAny(newWorkImage(img), w.ls, verbose)
}
