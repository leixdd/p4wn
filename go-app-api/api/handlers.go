package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Server bundles the HTTP handlers with their dependencies.
type Server struct {
	store  *Store
	pool   *Pool
	limits Limits
}

func NewServer(store *Store, pool *Pool, limits Limits) *Server {
	return &Server{store: store, pool: pool, limits: limits}
}

// Handler returns the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs", s.createJob)
	mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /v1/jobs/{id}/pages/{page}", s.getPage)
	mux.HandleFunc("GET /v1/jobs/{id}/document.html", s.getDocument)
	mux.HandleFunc("GET /v1/jobs/{id}/archive.zip", s.getArchive)
	mux.HandleFunc("DELETE /v1/jobs/{id}", s.deleteJob)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok\n")
	})
	return recoverMiddleware(mux)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// jobView is the wire representation of a job.
type jobView struct {
	ID          string       `json:"id"`
	Status      JobStatus    `json:"status"`
	Options     JobOptions   `json:"options"`
	PageCount   int          `json:"page_count,omitempty"`
	PagesDone   int          `json:"pages_done"`
	Pages       []PageResult `json:"pages,omitempty"`
	Error       string       `json:"error,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
	ExpiresAt   time.Time    `json:"expires_at"`
	StatusURL   string       `json:"status_url"`
	DocumentURL string       `json:"document_url,omitempty"`
}

func viewOf(j Job) jobView {
	v := jobView{
		ID: j.ID, Status: j.Status, Options: j.Options,
		PageCount: j.PageCount, Error: j.Error,
		CreatedAt: j.CreatedAt, ExpiresAt: j.ExpiresAt,
		StatusURL: "/v1/jobs/" + j.ID,
	}
	if !j.DoneAt.IsZero() {
		t := j.DoneAt
		v.CompletedAt = &t
	}
	html := j.Options.Format == FormatHTML
	for i := range j.Pages {
		pr := j.Pages[i]
		if pr.Status == PageDone {
			v.PagesDone++
			if !html {
				pr.URL = fmt.Sprintf("/v1/jobs/%s/pages/%d.png", j.ID, pr.Page)
			}
		}
		v.Pages = append(v.Pages, pr)
	}
	if html && (j.Status == StatusDone || j.Status == StatusPartial) && v.PagesDone > 0 {
		v.DocumentURL = "/v1/jobs/" + j.ID + "/" + DocumentFileName
	}
	return v
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	if s.store.Count() >= s.limits.MaxQueueDepth*2 {
		writeError(w, http.StatusTooManyRequests, "too many jobs, retry later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.limits.MaxUploadBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		}
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, `missing "file" form field`)
		return
	}
	defer file.Close()

	get := func(key string) string {
		if v := r.FormValue(key); v != "" {
			return v
		}
		return r.URL.Query().Get(key)
	}
	opts, err := ParseJobOptions(get, s.limits)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, err := s.store.Create(opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "job setup failed")
		return
	}
	dst, err := os.Create(filepath.Join(job.Dir, "input.pdf"))
	if err != nil {
		s.store.Delete(job.ID)
		writeError(w, http.StatusInternalServerError, "job setup failed")
		return
	}
	_, err = io.Copy(dst, file)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.store.Delete(job.ID)
		writeError(w, http.StatusInternalServerError, "storing upload failed")
		return
	}
	if !s.pool.Enqueue(job.ID) {
		s.store.Delete(job.ID)
		writeError(w, http.StatusTooManyRequests, "queue full, retry later")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":         job.ID,
		"status":     string(StatusQueued),
		"status_url": "/v1/jobs/" + job.ID,
	})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.store.Snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(job))
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	job, ok := s.store.Snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	if job.Options.Format == FormatHTML {
		writeError(w, http.StatusNotFound, "html jobs expose document.html, not per-page PNGs")
		return
	}
	pageStr := r.PathValue("page")
	pageStr = trimSuffix(pageStr, ".png")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		writeError(w, http.StatusBadRequest, "bad page number")
		return
	}
	for _, pr := range job.Pages {
		if pr.Page != page {
			continue
		}
		switch pr.Status {
		case PageDone:
			w.Header().Set("Content-Type", "image/png")
			http.ServeFile(w, r, filepath.Join(job.Dir, pageFileName(page)))
		case PageFailed:
			writeError(w, http.StatusUnprocessableEntity, "page failed: "+pr.Error)
		default:
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "page not rendered yet")
		}
		return
	}
	if job.Status == StatusQueued || job.Status == StatusProcessing {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "job still processing")
		return
	}
	writeError(w, http.StatusNotFound, "page not part of this job")
}

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) {
	job, ok := s.store.Snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	if job.Options.Format != FormatHTML {
		writeError(w, http.StatusNotFound, "job is not html format")
		return
	}
	if job.Status != StatusDone && job.Status != StatusPartial {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "job not finished")
		return
	}
	path := filepath.Join(job.Dir, DocumentFileName)
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "document not available")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, path)
}

func (s *Server) getArchive(w http.ResponseWriter, r *http.Request) {
	job, ok := s.store.Snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	if job.Status != StatusDone && job.Status != StatusPartial {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "job not finished")
		return
	}
	writeArchive(w, job)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.store.Snapshot(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	if job.Status == StatusQueued || job.Status == StatusProcessing {
		writeError(w, http.StatusConflict, "job is still running; try again when finished")
		return
	}
	s.store.Delete(job.ID)
	w.WriteHeader(http.StatusNoContent)
}

func trimSuffix(s, suffix string) string {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}
