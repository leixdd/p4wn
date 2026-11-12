package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Server bundles the HTTP handlers with their dependencies.
type Server struct {
	store   *Store
	pool    *Pool
	limits  Limits
	token   string   // bearer token; empty disables auth
	tickets *Tickets // nil = upload tickets disabled
	origins map[string]bool
}

// NewServer wires the handlers. A non-empty token requires
// "Authorization: Bearer <token>" on every route except /healthz.
func NewServer(store *Store, pool *Pool, limits Limits, token string) *Server {
	return &Server{store: store, pool: pool, limits: limits, token: token}
}

// EnableTickets makes POST /v1/jobs also accept "Authorization: Ticket <t>"
// (see Tickets). Call before Handler.
func (s *Server) EnableTickets(t *Tickets) { s.tickets = t }

// AllowOrigins lets browser pages on these exact origins call POST /v1/jobs
// (CORS). Call before Handler.
func (s *Server) AllowOrigins(origins []string) {
	s.origins = make(map[string]bool, len(origins))
	for _, o := range origins {
		s.origins[o] = true
	}
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
	return recoverMiddleware(s.corsMiddleware(s.authMiddleware(mux)))
}

// authMiddleware: "Authorization: Ticket <t>" is checked on POST /v1/jobs only
// and refused everywhere else; any other request needs the bearer token when
// one is set. Both happen before the request body is read.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if ticket, ok := strings.CutPrefix(auth, "Ticket "); ok {
			s.ticketAuth(w, r, ticket, next)
			return
		}
		if s.token != "" && subtle.ConstantTimeCompare([]byte(auth), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ticketAuth(w http.ResponseWriter, r *http.Request, ticket string, next http.Handler) {
	refuse := func(msg, jti string) {
		// never the ticket itself: it may still be valid
		log.Printf("ticket refused: %s %s jti=%q", r.Method, r.URL.Path, jti)
		w.Header().Set("WWW-Authenticate", "Ticket")
		writeError(w, http.StatusUnauthorized, msg)
	}
	if s.tickets == nil {
		refuse("upload tickets are not enabled", "")
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/jobs" {
		refuse("a ticket is only valid for POST /v1/jobs", "")
		return
	}
	c, err := s.tickets.Verify(ticket)
	if err != nil {
		log.Printf("ticket refused: %v jti=%q", err, c.JTI)
		w.Header().Set("WWW-Authenticate", "Ticket")
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	next.ServeHTTP(w, r.WithContext(withTicket(r.Context(), c)))
}

// corsMiddleware answers browser preflights for POST /v1/jobs from allowed
// origins (without auth) and marks /v1/jobs responses, errors included, as
// readable by them. Other routes never get CORS headers.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	if len(s.origins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if r.URL.Path != "/v1/jobs" || origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		allowed := s.origins[origin]
		if reqMethod := r.Header.Get("Access-Control-Request-Method"); r.Method == http.MethodOptions && reqMethod != "" {
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			if !allowed || reqMethod != http.MethodPost {
				w.WriteHeader(http.StatusForbidden) // no CORS headers: the browser blocks the request
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "POST")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if allowed {
			h.Set("Access-Control-Allow-Origin", origin)
		}
		next.ServeHTTP(w, r)
	})
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
	ticket, ticketed := ticketFrom(r.Context())
	fail := func(status int, msg string) {
		if ticketed {
			log.Printf("ticket jti=%q: upload refused (%d %s)", ticket.JTI, status, msg)
		}
		writeError(w, status, msg)
	}
	if s.store.Count() >= s.limits.MaxQueueDepth*2 {
		fail(http.StatusTooManyRequests, "too many jobs, retry later")
		return
	}
	maxBytes := s.limits.MaxUploadBytes
	if ticketed {
		// Job options come from the ticket only.
		for key := range r.URL.Query() {
			fail(http.StatusBadRequest, "a ticket request takes no parameters (job options come from the ticket): "+key)
			return
		}
		if ticket.MaxBytes > 0 && ticket.MaxBytes < maxBytes {
			maxBytes = ticket.MaxBytes
		}
	}
	// The limit is on the PDF itself; the body may exceed it by the multipart
	// framing and the small option fields.
	tooLarge := "upload too large (limit " + formatBytes(maxBytes) + ")"
	if r.ContentLength > maxBytes+multipartSlack {
		fail(http.StatusRequestEntityTooLarge, tooLarge) // refused before reading the body
		return
	}
	if ticketed {
		// Spent here, before the body is read: a second request with it fails
		// even while the first upload is still in flight.
		if err := s.tickets.Use(ticket); err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, errTicketCacheFull) {
				status = http.StatusServiceUnavailable
			}
			fail(status, err.Error())
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+multipartSlack)
	// File parts above 4 MB spool to a temp file, so request memory does not
	// grow with the upload limit.
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			fail(http.StatusRequestEntityTooLarge, tooLarge)
		} else {
			fail(http.StatusBadRequest, "invalid multipart form: "+err.Error())
		}
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, fh, err := r.FormFile("file")
	if err != nil {
		fail(http.StatusBadRequest, `missing "file" form field`)
		return
	}
	defer file.Close()
	if fh.Size > maxBytes {
		fail(http.StatusRequestEntityTooLarge, tooLarge)
		return
	}

	get := func(key string) string {
		if v := r.FormValue(key); v != "" {
			return v
		}
		return r.URL.Query().Get(key)
	}
	if ticketed {
		for key := range r.MultipartForm.Value {
			fail(http.StatusBadRequest, "a ticket request takes no parameters (job options come from the ticket): "+key)
			return
		}
		get = ticket.optionGetter()
	}
	opts, err := ParseJobOptions(get, s.limits)
	if err != nil {
		if ticketed {
			err = errors.New("ticket: " + err.Error())
		}
		fail(http.StatusBadRequest, err.Error())
		return
	}

	job, err := s.store.Create(opts)
	if err != nil {
		fail(http.StatusInternalServerError, "job setup failed")
		return
	}
	dst, err := os.Create(filepath.Join(job.Dir, "input.pdf"))
	if err != nil {
		s.store.Delete(job.ID)
		fail(http.StatusInternalServerError, "job setup failed")
		return
	}
	_, err = io.Copy(dst, file)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.store.Delete(job.ID)
		fail(http.StatusInternalServerError, "storing upload failed")
		return
	}
	if !s.pool.Enqueue(job.ID) {
		s.store.Delete(job.ID)
		fail(http.StatusTooManyRequests, "queue full, retry later")
		return
	}
	if ticketed {
		log.Printf("ticket jti=%q: accepted, job %s (%d bytes)", ticket.JTI, job.ID, fh.Size)
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
