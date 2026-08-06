package api

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// writeArchive streams every rendered artifact of the job as a zip.
func writeArchive(w http.ResponseWriter, job Job) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+job.ID+`.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()

	if job.Options.Format == FormatHTML {
		name := DocumentFileName
		f, err := os.Open(filepath.Join(job.Dir, name))
		if err != nil {
			return
		}
		entry, err := zw.Create(name)
		if err != nil {
			f.Close()
			return
		}
		_, err = io.Copy(entry, f)
		f.Close()
		_ = err
		return
	}

	for _, pr := range job.Pages {
		if pr.Status != PageDone {
			continue
		}
		name := pageFileName(pr.Page)
		f, err := os.Open(filepath.Join(job.Dir, name))
		if err != nil {
			continue
		}
		entry, err := zw.Create(name)
		if err != nil {
			f.Close()
			return // client connection is gone or zip is broken; stop
		}
		_, err = io.Copy(entry, f)
		f.Close()
		if err != nil {
			return
		}
	}
}
