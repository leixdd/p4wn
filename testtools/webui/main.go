// Command webui serves a browser frontend for manually exercising the
// p4wn API. It reverse-proxies /v1 and /healthz to the real API server so
// the page and the API share one origin — no CORS handling needed anywhere.
package main

import (
	_ "embed"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

//go:embed index.html
var indexHTML []byte

func main() {
	addr := flag.String("addr", ":3000", "listen address for the UI")
	api := flag.String("api", "http://localhost:8080", "p4wn API base URL")
	flag.Parse()

	target, err := url.Parse(*api)
	if err != nil {
		log.Fatalf("bad -api url: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	mux := http.NewServeMux()
	mux.Handle("/v1/", proxy)
	mux.Handle("/healthz", proxy)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	log.Printf("p4wn UI on http://localhost%s  →  API %s", *addr, *api)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
