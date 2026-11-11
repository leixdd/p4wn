// Command fidelity renders every page of every PDF in a directory through the
// same job pool p4wn-server uses and prints, per file: page count, failed
// pages (with error), near-blank pages (with ink coverage), per-page render
// warnings (images that failed to decode and were skipped), PDF features p4wn
// is known to degrade silently, and render time. Exits 1 if any page failed.
//
//	go run testtools/fidelity [-dpi 150] [-blank 0.001] dir/
//
// The renderer drops shadings and Type3 glyphs without reporting anything,
// so the "unsupported" column comes from a byte scan of the file (plus
// inflated object/content streams), not from the renderer. A scanned page
// whose only image was dropped shows up as near-blank and with a warning.
package main

import (
	"bytes"
	"compress/zlib"
	"flag"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"p4wn/api"
)

func main() {
	dpi := flag.Float64("dpi", 150, "render resolution")
	blank := flag.Float64("blank", 0.001, "near-blank if fewer than this fraction of pixels carry ink")
	pageWorkers := flag.Int("page-workers", 2, "concurrent pages per file")
	pageTimeout := flag.Duration("page-timeout", 60*time.Second, "per-page render timeout")
	cjkFont := flag.String("cjk-font", "", "CJK fallback font file, as p4wn-server -cjk-font")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: fidelity [flags] dir-of-pdfs")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if err := api.SetCJKFont(*cjkFont); err != nil {
		fatal(err)
	}
	entries, err := os.ReadDir(flag.Arg(0))
	if err != nil {
		fatal(err)
	}
	tmp, err := os.MkdirTemp("", "p4wn-fidelity-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(tmp)
	store, err := api.NewStore(tmp, time.Hour)
	if err != nil {
		fatal(err)
	}
	pool := api.NewPool(store, 1, *pageWorkers, 1<<30, 1, *pageTimeout)
	defer pool.Shutdown()

	var files, pages, failed, blanks int
	start := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
			continue
		}
		files++
		n, f, b := checkFile(store, pool, filepath.Join(flag.Arg(0), e.Name()), *dpi, *blank)
		pages, failed, blanks = pages+n, failed+f, blanks+b
	}
	fmt.Printf("TOTAL  files=%d pages=%d failed=%d near-blank=%d  %.2fs\n",
		files, pages, failed, blanks, time.Since(start).Seconds())
	if failed > 0 {
		pool.Shutdown()
		os.RemoveAll(tmp)
		os.Exit(1)
	}
}

// checkFile renders one PDF and prints its report line. A file that cannot
// be opened at all counts as one failed page.
func checkFile(store *api.Store, pool *api.Pool, path string, dpi, blankFrac float64) (pages, failed, blanks int) {
	name := filepath.Base(path)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("%s  ERROR %v\n", name, err)
		return 0, 1, 0
	}
	job, err := store.Create(api.JobOptions{DPI: dpi, Format: api.FormatPNG})
	if err != nil {
		fatal(err)
	}
	defer store.Delete(job.ID)
	if err := os.WriteFile(filepath.Join(job.Dir, "input.pdf"), data, 0o644); err != nil {
		fatal(err)
	}
	start := time.Now()
	if !pool.Enqueue(job.ID) {
		fatal(fmt.Errorf("enqueue %s: queue full", name))
	}
	var snap api.Job
	for {
		snap, _ = store.Snapshot(job.ID)
		if snap.Status == api.StatusDone || snap.Status == api.StatusPartial || snap.Status == api.StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(start)

	var failures, blankList, warnings []string
	for _, pr := range snap.Pages {
		for _, w := range pr.Warnings {
			warnings = append(warnings, fmt.Sprintf("p%d: %s", pr.Page, w))
		}
		if pr.Status != api.PageDone {
			failures = append(failures, fmt.Sprintf("p%d: %s", pr.Page, pr.Error))
			continue
		}
		ink, err := inkFraction(filepath.Join(job.Dir, fmt.Sprintf("page-%04d.png", pr.Page)))
		if err != nil {
			failures = append(failures, fmt.Sprintf("p%d: read png: %v", pr.Page, err))
		} else if ink < blankFrac {
			blankList = append(blankList, fmt.Sprintf("p%d(%.3f%%)", pr.Page, ink*100))
		}
	}
	if len(snap.Pages) == 0 && snap.Error != "" {
		failures = append(failures, snap.Error) // whole document failed to open
	}

	line := fmt.Sprintf("%s  pages=%d failed=%d near-blank=%d", name, snap.PageCount, len(failures), len(blankList))
	if len(blankList) > 0 {
		line += " [" + strings.Join(blankList, " ") + "]"
	}
	if feats := scanFeatures(data); len(feats) > 0 {
		line += "  unsupported=[" + strings.Join(feats, " ") + "]"
	}
	if encrypted(data) {
		line += "  (encrypted: feature scan partial)"
	}
	line += fmt.Sprintf("  %.2fs", elapsed.Seconds())
	if len(warnings) > 0 {
		line += "  warnings=[" + strings.Join(warnings, "; ") + "]"
	}
	if len(failures) > 0 {
		line += "  errors=[" + strings.Join(failures, "; ") + "]"
	}
	fmt.Println(line)
	return snap.PageCount, len(failures), len(blankList)
}

// inkFraction is the share of pixels noticeably darker than white, using
// testtools/inkprofile's darkness measure (alpha composited over white).
func inkFraction(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, err
	}
	b := img.Bounds()
	inked := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			af := float64(a) / 65535
			lum := (float64(r)+float64(g)+float64(bl))/3/65535*af + (1 - af)
			if 1-lum > 0.25 {
				inked++
			}
		}
	}
	return float64(inked) / float64(max(1, b.Dx()*b.Dy())), nil
}

// Features p4wn degrades without error (see go-app-api/README.md "Known
// degradations"). Image filter names live in stream dicts, which are never
// compressed; font dicts may sit in object streams, hence the inflate pass.
var features = []struct {
	label string
	re    *regexp.Regexp
}{
	{"JPX", regexp.MustCompile(`/JPXDecode\b`)},
	{"shading", regexp.MustCompile(`/ShadingType\b`)},
	{"Type3-font", regexp.MustCompile(`/Subtype\s*/Type3\b`)},
	{"Type1-font", regexp.MustCompile(`/FontFile\b`)},
}

var (
	cmapRe = regexp.MustCompile(`/Encoding\s*/([A-Za-z0-9_.+-]+-[HV])\b`)
	// predefined CMaps p4wn decodes (internal/font/cmap.go); others are
	// read as Identity
	knownCMap = regexp.MustCompile(`^(Identity|90ms-RKSJ|90msp-RKSJ|EUC|Uni(JIS|GB|CNS|KS)-(UCS2|UCS2-HW|UTF16|UTF8|UTF32))-[HV]$`)
	// descriptors without a FontFile* are non-embedded fonts: they fall back
	// to the Go fonts, which have no CJK glyphs, so CJK text there is blank
	// in PNG output unless -cjk-font is set (HTML still emits it as text).
	descRe = regexp.MustCompile(`/Type\s*/FontDescriptor\b`)
	fileRe = regexp.MustCompile(`/FontFile[23]?\b`)
)

func scanFeatures(data []byte) []string {
	corpus := [][]byte{data}
	for i := 0; ; {
		j := bytes.Index(data[i:], []byte("stream"))
		if j < 0 {
			break
		}
		i += j + len("stream")
		if i-len("endstream") >= 0 && string(data[i-len("endstream"):i]) == "endstream" {
			continue
		}
		start := i
		if start < len(data) && data[start] == '\r' {
			start++
		}
		if start < len(data) && data[start] == '\n' {
			start++
		}
		zr, err := zlib.NewReader(bytes.NewReader(data[start:]))
		if err != nil {
			continue
		}
		out, _ := io.ReadAll(io.LimitReader(zr, 4<<20)) // partial output is fine
		corpus = append(corpus, out)
	}

	counts := map[string]int{}
	descs, files := 0, 0
	for _, c := range corpus {
		for _, f := range features {
			counts[f.label] += len(f.re.FindAllIndex(c, -1))
		}
		for _, m := range cmapRe.FindAllSubmatch(c, -1) {
			if n := string(m[1]); !knownCMap.MatchString(n) {
				counts["cmap:"+n]++
			}
		}
		descs += len(descRe.FindAllIndex(c, -1))
		files += len(fileRe.FindAllIndex(c, -1))
	}
	counts["non-embedded-font"] = descs - files
	var out []string
	for label, n := range counts {
		if n > 0 {
			out = append(out, fmt.Sprintf("%s×%d", label, n))
		}
	}
	sort.Strings(out)
	return out
}

// encrypted files' streams can't be inflated, so font/CMap checks are partial.
func encrypted(data []byte) bool { return bytes.Contains(data, []byte("/Encrypt")) }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fidelity:", err)
	os.Exit(1)
}
