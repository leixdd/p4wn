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
	flag.Parse()

	limits := api.DefaultLimits()
	store, err := api.NewStore(*dataDir, *ttl)
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	stopSweep := make(chan struct{})
	store.StartSweeper(time.Minute, stopSweep)

	pool := api.NewPool(store, *jobWorkers, *pageWorkers, limits.MaxPages, limits.MaxQueueDepth, *pageTimeout)
	server := &http.Server{
		Addr:              *addr,
		Handler:           api.NewServer(store, pool, limits).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("p4wn-server listening on %s (data: %s)", *addr, *dataDir)
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
