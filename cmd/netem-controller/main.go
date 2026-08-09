package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/netem"
)

// starts the netem controller which listens for execution events from nodes and applies configured network emulation rules.
func main() {
	configPath := flag.String("config", "config/run2new.json", "PBFT experiment configuration")
	flag.Parse()

	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "netem controller is supported only on Linux")
		os.Exit(1)
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "netem controller must be started as root (use sudo)")
		os.Exit(1)
	}

	cfg := config.ReadCfg(*configPath)
	controllerLog := log.New(os.Stdout, "[NETEM] ", log.Ldate|log.Ltime|log.Lmicroseconds|log.LUTC)
	controller, err := netem.NewController(cfg, nil, controllerLog)
	if err != nil {
		controllerLog.Fatalf("create controller: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := controller.Run(ctx); err != nil {
		controllerLog.Fatalf("controller stopped with error: %v", err)
	}
	controllerLog.Print("controller stopped")
}
