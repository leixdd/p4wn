// p4wn-server is the async PDF→PNG conversion API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"p4wn/api"
)

func main() {
	addr := flag.String("addr", ":8081", "listen address")
	dataDir := flag.String("data-dir", "./data", "directory for job artifacts")
	ttl := flag.Duration("ttl", time.Hour, "job retention after completion")
	defWorkers := max(1, runtime.NumCPU()/2)
	jobWorkersFlag := flag.String("workers", "", fmt.Sprintf("concurrent jobs, 1..%d (default $P4WN_WORKERS, else max(1, CPUs/2) = %d)", api.WorkersCap, defWorkers))
	pageWorkersFlag := flag.String("page-workers", "", fmt.Sprintf("concurrent pages per job, 1..%d (default $P4WN_PAGE_WORKERS, else %d)", api.WorkersCap, api.DefaultPageWorkers))
	pageTimeout := flag.Duration("page-timeout", 60*time.Second, "per-page render timeout")
	limits := api.DefaultLimits()
	flag.Float64Var(&limits.MaxDPI, "max-dpi", limits.MaxDPI, "highest dpi (or 72·scale) a job may request")
	maxMPFlag := flag.String("max-megapixels", "", fmt.Sprintf("largest page (width·height, in millions of pixels) rendered, (0, %d]; bigger pages fail with an error instead of allocating (default $P4WN_MAX_MEGAPIXELS, else %g)", api.MaxMegapixelsCap, float64(limits.MaxPagePixels)/1e6))
	token := flag.String("token", "", "require 'Authorization: Bearer <token>' on all routes but /healthz (empty = no auth; default $P4WN_TOKEN)")
	cjkFont := flag.String("cjk-font", "", "TrueType/OpenType font or collection (.ttf/.otf/.ttc) used for CJK text in non-embedded fonts (empty = such text renders blank; default $P4WN_CJK_FONT)")
	maxUploadMB := flag.String("max-upload-mb", "", fmt.Sprintf("largest PDF accepted, in MB (2^20 bytes), 1..%d; bigger uploads get 413 (default $P4WN_MAX_UPLOAD_MB, else %d)", api.MaxUploadMBCap, api.DefaultMaxUploadMB))
	maxPages := flag.String("max-pages", "", fmt.Sprintf("most pages one job renders, 1..%d; a bigger selection fails the job before rendering (default $P4WN_MAX_PAGES, else %d)", api.MaxPagesCap, api.DefaultMaxPages))
	flag.Parse()
	// env fallback after Parse so -h never prints the token
	if *token == "" {
		*token = os.Getenv("P4WN_TOKEN")
	}
	if *cjkFont == "" {
		*cjkFont = os.Getenv("P4WN_CJK_FONT")
	}
	var err error
	v, src := flagOrEnv(*maxUploadMB, "-max-upload-mb", "P4WN_MAX_UPLOAD_MB", os.Getenv)
	if limits.MaxUploadBytes, err = api.ParseMaxUploadMB(v); err != nil {
		log.Fatalf("%s: %v", src, err)
	}
	v, src = flagOrEnv(*maxPages, "-max-pages", "P4WN_MAX_PAGES", os.Getenv)
	if limits.MaxPages, err = api.ParseMaxPages(v); err != nil {
		log.Fatalf("%s: %v", src, err)
	}
	v, src = flagOrEnv(*jobWorkersFlag, "-workers", "P4WN_WORKERS", os.Getenv)
	jobWorkers, err := api.ParseWorkers(v, defWorkers)
	if err != nil {
		log.Fatalf("%s: %v", src, err)
	}
	v, src = flagOrEnv(*pageWorkersFlag, "-page-workers", "P4WN_PAGE_WORKERS", os.Getenv)
	pageWorkers, err := api.ParseWorkers(v, api.DefaultPageWorkers)
	if err != nil {
		log.Fatalf("%s: %v", src, err)
	}
	v, src = flagOrEnv(*maxMPFlag, "-max-megapixels", "P4WN_MAX_MEGAPIXELS", os.Getenv)
	if limits.MaxPagePixels, err = api.ParseMaxMegapixels(v); err != nil {
		log.Fatalf("%s: %v", src, err)
	}
	// Upload tickets and CORS: env only (the secret must not show in ps).
	var tickets *api.Tickets
	if secret := os.Getenv("P4WN_TICKET_SECRET"); secret != "" {
		if *token == "" {
			log.Fatal("P4WN_TICKET_SECRET needs a bearer token (-token / P4WN_TOKEN): without one every route is open and a ticket restricts nothing")
		}
		if tickets, err = api.NewTickets(secret); err != nil {
			log.Fatalf("P4WN_TICKET_SECRET: %v", err)
		}
	}
	corsOrigins, err := api.ParseCORSOrigins(os.Getenv("P4WN_CORS_ORIGINS"))
	if err != nil {
		log.Fatalf("P4WN_CORS_ORIGINS: %v", err)
	}
	if !(limits.MaxDPI >= limits.MinDPI) {
		log.Fatalf("-max-dpi %g is below the minimum dpi %g", limits.MaxDPI, limits.MinDPI)
	}

	if *cjkFont != "" {
		if err := api.SetCJKFont(*cjkFont); err != nil {
			log.Fatalf("-cjk-font: %v", err)
		}
	}

	store, err := api.NewStore(*dataDir, *ttl)
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	stopSweep := make(chan struct{})
	store.StartSweeper(time.Minute, stopSweep)

	pool := api.NewPool(store, jobWorkers, pageWorkers, limits.MaxPages, limits.MaxQueueDepth, *pageTimeout)
	pool.MaxPagePixels = limits.MaxPagePixels
	s3, err := api.S3FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if s3 != nil {
		pool.S3 = s3
		log.Printf("uploading png pages to s3 bucket %q at %s", s3.Bucket, s3.Endpoint)
	}
	srv := api.NewServer(store, pool, limits, *token)
	if tickets != nil {
		srv.EnableTickets(tickets)
		tickets.StartSweeper(time.Minute, stopSweep)
	}
	srv.AllowOrigins(corsOrigins)
	server := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// whole request incl. a full-size upload at ~1.4 Mbit/s (6 s/MB,
		// 10 min at 100 MB). No WriteTimeout on purpose — large archive.zip
		// downloads must finish.
		ReadTimeout: api.ReadTimeoutFor(limits.MaxUploadBytes),
		IdleTimeout: 2 * time.Minute,
	}

	go func() {
		log.Printf("p4wn-server listening on %s (data: %s, max-upload: %d MB, read-timeout: %s, max-pages: %d, max-dpi: %g, max-megapixels: %g, workers: %d, page-workers: %d, auth: %t, tickets: %t, cors-origins: %q, cjk-font: %q)", *addr, *dataDir, limits.MaxUploadBytes>>20, server.ReadTimeout, limits.MaxPages, limits.MaxDPI, float64(limits.MaxPagePixels)/1e6, jobWorkers, pageWorkers, *token != "", tickets != nil, corsOrigins, *cjkFont)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")
	close(stopSweep)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	pool.Shutdown()
}

// flagOrEnv returns the flag's value when it was given (non-empty), else the
// environment variable's, with the name to blame in a startup error.
func flagOrEnv(flagVal, flagName, env string, getenv func(string) string) (val, src string) {
	if flagVal != "" {
		return flagVal, flagName
	}
	return getenv(env), env
}
