// Package api implements the async pdf→png/html HTTP service.
package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"p4wn/internal/content"
	"p4wn/internal/font"
)

// Limits bound resource usage per request/job.
type Limits struct {
	MaxUploadBytes int64 // largest PDF accepted by POST /v1/jobs; server -max-upload-mb
	MaxPages       int   // most pages one job renders; server -max-pages
	MaxQueueDepth  int
	MinDPI         float64
	MaxDPI         float64
	MaxPagePixels  int // per rendered page; server -max-megapixels
}

const (
	// DefaultMaxUploadMB is the upload limit when neither -max-upload-mb nor
	// P4WN_MAX_UPLOAD_MB is given. 1 MB = 2^20 bytes throughout.
	DefaultMaxUploadMB = 100
	// MaxUploadMBCap bounds -max-upload-mb: a job holds its whole input in
	// memory while it runs, on top of the page pixmaps.
	MaxUploadMBCap = 1024
	// DefaultMaxPages is the per-job page limit when neither -max-pages nor
	// P4WN_MAX_PAGES is given.
	DefaultMaxPages = 1000
	// MaxPagesCap bounds -max-pages: every selected page keeps a status entry
	// (in memory and in GET /v1/jobs/{id}) and a PNG on disk until the job
	// expires.
	MaxPagesCap = 10000
	// DefaultPageWorkers is the per-job page concurrency when neither
	// -page-workers nor P4WN_PAGE_WORKERS is given.
	DefaultPageWorkers = 4
	// WorkersCap bounds -workers and -page-workers. Rendering is CPU-bound,
	// so counts far above the core count only add memory.
	WorkersCap = 256
	// MaxMegapixelsCap bounds -max-megapixels (graphics.MaxPixmapDim²).
	MaxMegapixelsCap = 400
	// uploadSecondsPerMB sizes the server read timeout: 6 s/MB ≈ 1.4 Mbit/s,
	// the slowest client a full-size upload must still work for.
	uploadSecondsPerMB = 6
	// multipartSlack is what a POST /v1/jobs body may carry beyond the file:
	// multipart framing and the option fields.
	multipartSlack = 64 << 10
)

func DefaultLimits() Limits {
	return Limits{
		MaxUploadBytes: DefaultMaxUploadMB << 20,
		MaxPages:       DefaultMaxPages,
		MaxQueueDepth:  100,
		MinDPI:         8,
		MaxDPI:         300, // server -max-dpi; Letter @1200 dpi needs ~1 GB of pixmaps
		MaxPagePixels:  content.DefaultMaxPagePixels,
	}
}

// ParseMaxUploadMB validates a -max-upload-mb / P4WN_MAX_UPLOAD_MB value
// (whole MB, 1..MaxUploadMBCap) and returns it in bytes. Empty = the default.
func ParseMaxUploadMB(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultMaxUploadMB << 20, nil
	}
	mb, err := strconv.Atoi(s)
	if err != nil || mb < 1 || mb > MaxUploadMBCap {
		return 0, fmt.Errorf("%q is not a whole number of MB in [1, %d]", s, MaxUploadMBCap)
	}
	return int64(mb) << 20, nil
}

// ParseMaxPages validates a -max-pages / P4WN_MAX_PAGES value (whole number,
// 1..MaxPagesCap). Empty = the default.
func ParseMaxPages(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultMaxPages, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxPagesCap {
		return 0, fmt.Errorf("%q is not a whole number of pages in [1, %d]", s, MaxPagesCap)
	}
	return n, nil
}

// ParseWorkers validates a -workers / -page-workers value (or its
// P4WN_WORKERS / P4WN_PAGE_WORKERS fallback): a whole number in
// [1, WorkersCap]. Empty = def.
func ParseWorkers(s string, def int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > WorkersCap {
		return 0, fmt.Errorf("%q is not a whole number in [1, %d]", s, WorkersCap)
	}
	return n, nil
}

// ParseMaxMegapixels validates a -max-megapixels / P4WN_MAX_MEGAPIXELS value
// (millions of pixels, in (0, MaxMegapixelsCap]) and returns it in pixels.
// Empty = content.DefaultMaxPagePixels.
func ParseMaxMegapixels(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return content.DefaultMaxPagePixels, nil
	}
	mp, err := strconv.ParseFloat(s, 64)
	// !(…) also rejects NaN; under one pixel would read as 0 = the default
	if err != nil || !(mp > 0 && mp <= MaxMegapixelsCap) || int(mp*1e6) < 1 {
		return 0, fmt.Errorf("%q is not a number of megapixels in (0, %d]", s, MaxMegapixelsCap)
	}
	return int(mp * 1e6), nil
}

// ReadTimeoutFor is the server's whole-request read timeout for an upload
// limit: the full-size upload at ~1.4 Mbit/s (6 s/MB; 100 MB → 10 min), at
// least 1 min.
func ReadTimeoutFor(maxUploadBytes int64) time.Duration {
	mb := (maxUploadBytes + 1<<20 - 1) >> 20
	return max(time.Minute, time.Duration(mb*uploadSecondsPerMB)*time.Second)
}

// formatBytes prints a size as "N MB" when it is whole MB, else in bytes.
func formatBytes(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// SetCJKFont configures the process-wide fallback font for CJK text in
// non-embedded fonts (see font.SetCJKFallback). Call before rendering.
func SetCJKFont(path string) error { return font.SetCJKFallback(path) }

// OutputFormat selects the job artifact type.
type OutputFormat string

const (
	FormatPNG  OutputFormat = "png"
	FormatHTML OutputFormat = "html"
)

// JobOptions are the user-selectable render options for one job.
type JobOptions struct {
	DPI    float64      `json:"dpi"`
	Pages  string       `json:"pages,omitempty"` // raw selection string; empty = all
	Gray   bool         `json:"gray"`
	Alpha  bool         `json:"alpha"`
	Format OutputFormat `json:"format"`
	// KeyPrefix names the S3 objects of a png job: "<KeyPrefix>-<page>.png".
	// Empty = the job ID. Only used when the server has a bucket configured.
	KeyPrefix string `json:"key_prefix,omitempty"`
	// MaxPages fails the job before rendering when the document has more
	// pages than this (0 = only the server limit applies).
	MaxPages int `json:"max_pages,omitempty"`
}

// ParseJobOptions validates form/query values into JobOptions.
// Accepted keys: dpi, scale (dpi = 72·scale), pages, gray, alpha, format,
// key_prefix, max_pages.
func ParseJobOptions(get func(string) string, lim Limits) (JobOptions, error) {
	opts := JobOptions{DPI: 150, Format: FormatPNG}
	if s := get("dpi"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return opts, fmt.Errorf("invalid dpi %q", s)
		}
		opts.DPI = v
	} else if s := get("scale"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return opts, fmt.Errorf("invalid scale %q", s)
		}
		opts.DPI = 72 * v
	}
	// scale is folded into DPI above, so this bound covers both; the negated
	// form also rejects NaN, which compares false against everything.
	if !(opts.DPI >= lim.MinDPI && opts.DPI <= lim.MaxDPI) {
		return opts, fmt.Errorf("dpi %.4g out of range [%g, %g] (scale = dpi/72)", opts.DPI, lim.MinDPI, lim.MaxDPI)
	}
	opts.Pages = get("pages")
	if opts.Pages != "" {
		// syntax check now; resolved against the real page count later
		if _, err := ParsePageRanges(opts.Pages, 1<<30); err != nil {
			return opts, err
		}
	}
	var err error
	if opts.Gray, err = parseBool(get("gray")); err != nil {
		return opts, fmt.Errorf("invalid gray value")
	}
	if opts.Alpha, err = parseBool(get("alpha")); err != nil {
		return opts, fmt.Errorf("invalid alpha value")
	}
	switch strings.ToLower(strings.TrimSpace(get("format"))) {
	case "", "png":
		opts.Format = FormatPNG
	case "html":
		opts.Format = FormatHTML
	default:
		return opts, fmt.Errorf("invalid format %q (want png or html)", get("format"))
	}
	if s := get("max_pages"); s != "" {
		if opts.MaxPages, err = strconv.Atoi(s); err != nil || opts.MaxPages < 1 {
			return opts, fmt.Errorf("invalid max_pages %q", s)
		}
	}
	opts.KeyPrefix = get("key_prefix")
	if opts.KeyPrefix != "" && (len(opts.KeyPrefix) > 200 || !keyPrefixRe.MatchString(opts.KeyPrefix)) {
		return opts, fmt.Errorf("invalid key_prefix (want [A-Za-z0-9_-] segments separated by /, max 200 chars)")
	}
	return opts, nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	return false, fmt.Errorf("bad bool %q", s)
}

// ParsePageRanges expands a selection like "1-3,7,9-" (1-based, inclusive,
// open-ended allowed) into sorted unique 0-based page indices, clamped to
// numPages. Empty selects all pages.
func ParsePageRanges(sel string, numPages int) ([]int, error) {
	if numPages <= 0 {
		return nil, fmt.Errorf("document has no pages")
	}
	if strings.TrimSpace(sel) == "" {
		all := make([]int, numPages)
		for i := range all {
			all[i] = i
		}
		return all, nil
	}
	include := make(map[int]bool)
	for _, part := range strings.Split(sel, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi := 1, numPages
		if dash := strings.IndexByte(part, '-'); dash >= 0 {
			loStr := strings.TrimSpace(part[:dash])
			hiStr := strings.TrimSpace(part[dash+1:])
			var err error
			if loStr != "" {
				if lo, err = strconv.Atoi(loStr); err != nil {
					return nil, fmt.Errorf("invalid page range %q", part)
				}
			}
			if hiStr != "" {
				if hi, err = strconv.Atoi(hiStr); err != nil {
					return nil, fmt.Errorf("invalid page range %q", part)
				}
			}
		} else {
			v, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid page number %q", part)
			}
			lo, hi = v, v
		}
		if lo < 1 {
			lo = 1
		}
		if hi > numPages {
			hi = numPages
		}
		for p := lo; p <= hi; p++ {
			include[p-1] = true
		}
	}
	if len(include) == 0 {
		return nil, fmt.Errorf("page selection %q matches no pages", sel)
	}
	out := make([]int, 0, len(include))
	for p := range include {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}
