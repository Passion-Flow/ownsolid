//go:build linux

// Command egress-recorder creates a TUN interface and records every packet routed to it
// as an outbound attempt. tests/e2e/run-single-node.sh starts it inside the isolated
// Docker-in-Docker host and routes all non-local traffic to the interface.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Passion-Flow/ownsolid/tests/e2e/egress"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "egress-recorder:", err)
		os.Exit(1)
	}
}

func run() error {
	name := flag.String("interface", "egress0", "name of the TUN interface to create")
	logPath := flag.String("log", "", "file that receives one JSON line per outbound attempt")
	flag.Parse()
	if *logPath == "" {
		return errors.New("-log is required")
	}
	log, err := os.OpenFile(*logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	device, err := egress.OpenTUN(*name)
	if err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signals
		device.Close()
	}()
	fmt.Fprintf(os.Stderr, "egress-recorder: recording on %s into %s\n", *name, *logPath)
	err = egress.NewRecorder(device, log).Run()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
