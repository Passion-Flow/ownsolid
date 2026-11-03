// This executable is an explicit synthetic integration fixture, never a product service.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const maximumFixtureLifetime = 2 * time.Minute

func main() {
	listen := flag.String("listen", "", "Explicit synthetic HTTP listener")
	lifetime := flag.Duration("lifetime", 0, "Required bounded fixture lifetime")
	shutdown := flag.Duration("shutdown-timeout", 0, "Required graceful shutdown bound")
	flag.Parse()
	if *listen == "" || *lifetime <= 0 || *lifetime > maximumFixtureLifetime || *shutdown <= 0 || *shutdown >= *lifetime {
		fmt.Fprintln(os.Stderr, "SYNTHETIC_PROCESS_CONFIGURATION_INVALID")
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/fixture-health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"fixture":"explicit_synthetic_process"}`)
	})
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: *shutdown}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	timer := time.NewTimer(*lifetime)
	defer timer.Stop()
	select {
	case e := <-failures:
		if e != nil && e != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "SYNTHETIC_PROCESS_LISTENER_FAILED")
			os.Exit(1)
		}
		return
	case <-ctx.Done():
	case <-timer.C:
	}
	stop, done := context.WithTimeout(context.Background(), *shutdown)
	defer done()
	if server.Shutdown(stop) != nil {
		fmt.Fprintln(os.Stderr, "SYNTHETIC_PROCESS_SHUTDOWN_FAILED")
		os.Exit(1)
	}
}
