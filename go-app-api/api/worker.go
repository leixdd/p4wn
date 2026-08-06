package api

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"p4wn/internal/content"
	"p4wn/internal/pdf"
)

// Pool renders queued jobs. jobWorkers jobs run concurrently; within a job,
// pageWorkers pages render concurrently (pages are independent).
type Pool struct {
	store       *Store
	queue       chan string
	pageWorkers int
	pageTimeout time.Duration
	maxPages    int
	wg          sync.WaitGroup
	cancel      context.CancelFunc
}

func NewPool(store *Store, jobWorkers, pageWorkers, maxPages int, queueDepth int, pageTimeout time.Duration) *Pool {
	if jobWorkers < 1 {
		jobWorkers = 1
	}
	if pageWorkers < 1 {
		pageWorkers = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		store:       store,
		queue:       make(chan string, queueDepth),
		pageWorkers: pageWorkers,
		pageTimeout: pageTimeout,
		maxPages:    maxPages,
		cancel:      cancel,
	}
	for i := 0; i < jobWorkers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id, ok := <-p.queue:
					if !ok {
						return
					}
					p.runJob(ctx, id)
				}
			}
		}()
	}
	return p
}

// Enqueue queues a job; false when the queue is full.
func (p *Pool) Enqueue(id string) bool {
	select {
	case p.queue <- id:
		return true
	default:
		return false
	}
}

// Shutdown stops accepting work and waits for in-flight jobs.
func (p *Pool) Shutdown() {
	p.cancel()
	p.wg.Wait()
}

func (p *Pool) runJob(ctx context.Context, id string) {
	snap, ok := p.store.Snapshot(id)
	if !ok {
		return // deleted while queued
	}
	p.store.Update(id, func(j *Job) { j.Status = StatusProcessing })

	fail := func(msg string) {
		p.store.Update(id, func(j *Job) {
			j.Status = StatusFailed
			j.Error = msg
			j.DoneAt = time.Now()
		})
	}

	data, err := os.ReadFile(filepath.Join(snap.Dir, "input.pdf"))
	if err != nil {
		fail("input vanished: " + err.Error())
		return
	}
	doc, err := openDoc(data)
	if err != nil {
		fail(err.Error())
		return
	}
	selected, err := ParsePageRanges(snap.Options.Pages, doc.NumPages())
	if err != nil {
		fail(err.Error())
		return
	}
	if len(selected) > p.maxPages {
		fail(fmt.Sprintf("selection has %d pages, limit is %d", len(selected), p.maxPages))
		return
	}

	results := make([]PageResult, len(selected))
	for i, pg := range selected {
		results[i] = PageResult{Page: pg + 1, Status: PagePending}
	}
	p.store.Update(id, func(j *Job) {
		j.PageCount = doc.NumPages()
		j.Pages = append([]PageResult(nil), results...)
	})

	if snap.Options.Format == FormatHTML {
		p.runHTMLJob(ctx, id, doc, snap, selected)
		return
	}

	// fan pages out to pageWorkers goroutines
	type task struct{ idx, page int }
	tasks := make(chan task)
	var wg sync.WaitGroup
	for w := 0; w < p.pageWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				res := p.renderPage(ctx, doc, snap, t.page)
				p.store.Update(id, func(j *Job) {
					if t.idx < len(j.Pages) {
						j.Pages[t.idx] = res
					}
				})
			}
		}()
	}
	for i, pg := range selected {
		select {
		case <-ctx.Done():
		case tasks <- task{idx: i, page: pg}:
		}
	}
	close(tasks)
	wg.Wait()

	p.finishJob(id)
}

func (p *Pool) runHTMLJob(ctx context.Context, id string, doc *pdf.Document, snap Job, selected []int) {
	type task struct{ idx, page int }
	type outcome struct {
		idx  int
		res  PageResult
		frag content.PageHTML
		ok   bool
	}
	tasks := make(chan task)
	outcomes := make(chan outcome, len(selected))
	var wg sync.WaitGroup
	for w := 0; w < p.pageWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				res, frag := p.renderHTMLPage(ctx, doc, t.page)
				outcomes <- outcome{idx: t.idx, res: res, frag: frag, ok: res.Status == PageDone}
			}
		}()
	}
	for i, pg := range selected {
		select {
		case <-ctx.Done():
		case tasks <- task{idx: i, page: pg}:
		}
	}
	close(tasks)
	wg.Wait()
	close(outcomes)

	frags := make([]content.PageHTML, len(selected))
	have := make([]bool, len(selected))
	for o := range outcomes {
		p.store.Update(id, func(j *Job) {
			if o.idx < len(j.Pages) {
				j.Pages[o.idx] = o.res
			}
		})
		if o.ok {
			frags[o.idx] = o.frag
			have[o.idx] = true
		}
	}

	assembled := make([]content.PageHTML, 0, len(selected))
	for i := range selected {
		if have[i] {
			assembled = append(assembled, frags[i])
		}
	}
	if len(assembled) > 0 {
		html := content.AssembleHTMLDocument(assembled)
		name := filepath.Join(snap.Dir, DocumentFileName)
		if err := os.WriteFile(name, []byte(html), 0o644); err != nil {
			p.store.Update(id, func(j *Job) {
				j.Status = StatusFailed
				j.Error = "write document.html: " + err.Error()
				j.DoneAt = time.Now()
			})
			return
		}
	}
	p.finishJob(id)
}

func (p *Pool) finishJob(id string) {
	p.store.Update(id, func(j *Job) {
		done, failed := 0, 0
		for _, pr := range j.Pages {
			switch pr.Status {
			case PageDone:
				done++
			case PageFailed:
				failed++
			}
		}
		switch {
		case done == 0:
			j.Status = StatusFailed
			if j.Error == "" {
				j.Error = "all pages failed to render"
			}
		case failed > 0:
			j.Status = StatusPartial
		default:
			j.Status = StatusDone
		}
		j.DoneAt = time.Now()
	})
}

// renderPage renders one page with panic isolation and a timeout: a renderer
// bug fails that page, never the process.
func (p *Pool) renderPage(ctx context.Context, doc *pdf.Document, job Job, page int) (res PageResult) {
	res = PageResult{Page: page + 1}
	defer func() {
		if r := recover(); r != nil {
			res.Status = PageFailed
			res.Error = fmt.Sprintf("renderer panic: %v", r)
			debug.PrintStack()
		}
	}()
	ctx, cancelTimeout := context.WithTimeout(ctx, p.pageTimeout)
	defer cancelTimeout()

	pix, err := content.RenderPage(ctx, doc, page, content.RenderOptions{
		DPI:   job.Options.DPI,
		Gray:  job.Options.Gray,
		Alpha: job.Options.Alpha,
	})
	if err != nil {
		res.Status = PageFailed
		res.Error = err.Error()
		return res
	}
	name := filepath.Join(job.Dir, pageFileName(page+1))
	f, err := os.Create(name)
	if err != nil {
		res.Status = PageFailed
		res.Error = err.Error()
		return res
	}
	err = png.Encode(f, pix.ToImage())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(name)
		res.Status = PageFailed
		res.Error = err.Error()
		return res
	}
	res.Status = PageDone
	res.Width, res.Height = pix.W, pix.H
	return res
}

func (p *Pool) renderHTMLPage(ctx context.Context, doc *pdf.Document, page int) (res PageResult, frag content.PageHTML) {
	res = PageResult{Page: page + 1}
	defer func() {
		if r := recover(); r != nil {
			res.Status = PageFailed
			res.Error = fmt.Sprintf("renderer panic: %v", r)
			debug.PrintStack()
		}
	}()
	ctx, cancelTimeout := context.WithTimeout(ctx, p.pageTimeout)
	defer cancelTimeout()

	frag, err := content.RenderPageHTML(ctx, doc, page)
	if err != nil {
		res.Status = PageFailed
		res.Error = err.Error()
		return res, frag
	}
	res.Status = PageDone
	res.Width = int(frag.Width + 0.5)
	res.Height = int(frag.Height + 0.5)
	return res, frag
}

func pageFileName(page1 int) string { return fmt.Sprintf("page-%04d.png", page1) }

// openDoc isolates pdf.Open panics (hostile files) into errors.
func openDoc(data []byte) (doc *pdf.Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("open pdf: parser panic: %v", r)
		}
	}()
	return pdf.Open(data)
}
