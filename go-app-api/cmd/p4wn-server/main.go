// p4wn-server is the async PDF→PNG conversion API.
package main

import (
	"context"
	"errors"
	"flag"
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
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data-dir", "./data", "directory for job artifacts")
	ttl := flag.Duration("ttl", time.Hour, "job retention after completion")
	jobWorkers := flag.Int("workers", max(1, runtime.NumCPU()/2), "concurrent jobs")
	pageWorkers := flag.Int("page-workers", 4, "concurrent pages per job")
	pageTimeout := flag.Duration("page-timeout", 60*time.Second, "per-page render timeout")
	limits := api.DefaultLimits()
	flag.Float64Var(&limits.MaxDPI, "max-dpi", limits.MaxDPI, "highest dpi (or 72·scale) a job may request")
	maxMP := flag.Float64("max-megapixels", float64(limits.MaxPagePixels)/1e6, "largest page (width·height, in millions of pixels) rendered; bigger pages fail with an error instead of allocating")
	token := flag.String("token", "", "require 'Authorization: Bearer <token>' on all routes but /healthz (empty = no auth)")
	cjkFont := flag.String("cjk-font", "", "TrueType/OpenType font or collection (.ttf/.otf/.ttc) used for CJK text in non-embedded fonts (empty = such text renders blank)")
	flag.Parse()
	if !(limits.MaxDPI >= limits.MinDPI) {
		log.Fatalf("-max-dpi %g is below the minimum dpi %g", limits.MaxDPI, limits.MinDPI)
	}
	if !(*maxMP > 0 && *maxMP <= 400) { // 400 MP = graphics.MaxPixmapDim² bound
		log.Fatalf("-max-megapixels %g out of range (0, 400]", *maxMP)
	}
	limits.MaxPagePixels = int(*maxMP * 1e6)

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

	pool := api.NewPool(store, *jobWorkers, *pageWorkers, limits.MaxPages, limits.MaxQueueDepth, *pageTimeout)
	pool.MaxPagePixels = limits.MaxPagePixels
	server := &http.Server{
		Addr:              *addr,
		Handler:           api.NewServer(store, pool, limits, *token).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// whole request incl. a 50 MB upload: ~5 min ≈ 1.4 Mbit/s floor.
		// No WriteTimeout on purpose — large archive.zip downloads must finish.
		ReadTimeout: 5 * time.Minute,
		IdleTimeout: 2 * time.Minute,
	}

	go func() {
		log.Printf("p4wn-server listening on %s (data: %s, max-dpi: %g, max-megapixels: %g, auth: %t, cjk-font: %q)", *addr, *dataDir, limits.MaxDPI, *maxMP, *token != "", *cjkFont)
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
