package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "pdfs", name))
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	return data
}

func submit(t *testing.T, srv *Server, pdf []byte, fields map[string]string) jobView {
	t.Helper()
	h := srv.Handler()
	res := postJob(t, h, pdf, fields)
	var created map[string]string
	if err := readJSON(res.Body, &created); err != nil || created["id"] == "" {
		t.Fatalf("create: %d %v %v", res.StatusCode, created, err)
	}
	return waitDone(t, h, created["id"])
}

// TestPagePixelCap: an oversized page fails (job partial/failed) with the
// cap in its error instead of allocating.
func TestPagePixelCap(t *testing.T) {
	srv, _, pool := testServer(t)
	pool.MaxPagePixels = 1_000_000
	v := submit(t, srv, readFixture(t, "large-format-a0.pdf"), map[string]string{"dpi": "72"})
	if v.Status != StatusFailed && v.Status != StatusPartial || len(v.Pages) != 1 {
		t.Fatalf("status %s pages %+v", v.Status, v.Pages)
	}
	if p := v.Pages[0]; p.Status != PageFailed || !strings.Contains(p.Error, "over the 1.0 MP page limit") {
		t.Fatalf("page = %+v", p)
	}
}

// TestPageWarnings: an undecodable image is reported on the done page.
func TestPageWarnings(t *testing.T) {
	srv, _, _ := testServer(t)
	v := submit(t, srv, readFixture(t, "ccitt.pdf"), map[string]string{"dpi": "36"})
	if v.Status != StatusDone || len(v.Pages) != 1 {
		t.Fatalf("status %s pages %+v", v.Status, v.Pages)
	}
	if w := v.Pages[0].Warnings; len(w) != 1 || !strings.Contains(w[0], "Group 3 2D") {
		t.Fatalf("warnings = %q", w)
	}
}
