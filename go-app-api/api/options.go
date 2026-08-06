// Package api implements the async pdf→png/html HTTP service.
package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Limits bound resource usage per request/job.
type Limits struct {
	MaxUploadBytes int64
	MaxPages       int
	MaxQueueDepth  int
	MinDPI         float64
	MaxDPI         float64
}

func DefaultLimits() Limits {
	return Limits{
		MaxUploadBytes: 50 << 20,
		MaxPages:       500,
		MaxQueueDepth:  100,
		MinDPI:         8,
		MaxDPI:         1200,
	}
}

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
}

// ParseJobOptions validates form/query values into JobOptions.
// Accepted keys: dpi, scale (dpi = 72·scale), pages, gray, alpha, format.
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
	if opts.DPI < lim.MinDPI || opts.DPI > lim.MaxDPI {
		return opts, fmt.Errorf("dpi %.4g out of range [%g, %g]", opts.DPI, lim.MinDPI, lim.MaxDPI)
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
