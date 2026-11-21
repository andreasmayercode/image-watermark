package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"time"

	watermark "github.com/andreasmayercode/image-watermark"
	"github.com/andreasmayercode/image-watermark/web"
)

func (cfg Config) serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP address")
	maxMB := fs.Int64("max-mb", 30, "upload limit in MB")
	kf := addKeyFlags(fs)
	fs.Parse(args)
	wm, err := cfg.watermarker(kf)
	if err != nil {
		return err
	}
	indexHTML := cfg.IndexHTML
	if indexHTML == nil {
		indexHTML = web.IndexHTML
	}
	sem := make(chan struct{}, 2) // at most two detections at a time

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	detect := func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"status": "error", "error": msg})
		}
		r.Body = http.MaxBytesReader(w, r.Body, *maxMB<<20)
		f, _, err := r.FormFile("image")
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				fail(http.StatusRequestEntityTooLarge, "file too large")
			} else {
				fail(http.StatusBadRequest, "no image uploaded")
			}
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			fail(http.StatusBadRequest, "upload failed")
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-time.After(20 * time.Second):
			fail(http.StatusServiceUnavailable, "busy, please try again")
			return
		}
		src, err := watermark.Decode(data)
		if err != nil {
			fail(http.StatusUnprocessableEntity, err.Error())
			return
		}
		res := wm.Detect(src.Image, false)
		json.NewEncoder(w).Encode(cfg.withText(res, res.ID))
	}
	mux.HandleFunc("POST /detect", detect)
	mux.HandleFunc("POST /detect.php", detect) // same URL as the PHP variant

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	log.Printf("%s %s listening on http://%s/", cfg.Name, cfg.Version, *listen)
	return srv.ListenAndServe()
}

func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src blob: data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
}
