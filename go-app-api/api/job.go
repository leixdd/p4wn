package api

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type JobStatus string

const (
	StatusQueued     JobStatus = "queued"
	StatusProcessing JobStatus = "processing"
	StatusDone       JobStatus = "done"
	StatusPartial    JobStatus = "partial" // some pages rendered, some failed
	StatusFailed     JobStatus = "failed"  // nothing usable produced
)

type PageStatus string

const (
	PagePending PageStatus = "pending"
	PageDone    PageStatus = "done"
	PageFailed  PageStatus = "failed"
)

// PageResult tracks one selected page of a job.
type PageResult struct {
	Page   int        `json:"page"` // 1-based page number
	Status PageStatus `json:"status"`
	Width  int        `json:"width,omitempty"`
	Height int        `json:"height,omitempty"`
	Error  string     `json:"error,omitempty"`
	URL    string     `json:"url,omitempty"`
}

// Job is one conversion request. Fields are guarded by the Store mutex —
// handlers read snapshots via Store.Snapshot.
type Job struct {
	ID        string
	Status    JobStatus
	Options   JobOptions
	PageCount int          // total pages in the document (0 until parsed)
	Pages     []PageResult // one per *selected* page
	Error     string
	CreatedAt time.Time
	DoneAt    time.Time
	ExpiresAt time.Time

	Dir string // disk directory holding input.pdf and page PNGs / document.html
}

// DocumentFileName is the assembled HTML artifact for format=html jobs.
const DocumentFileName = "document.html"

// NewJobID returns a 16-byte random hex ID.
func NewJobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b[:])
}
