package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/durck/reverse_logger/internal/edgehealth"
)

func main() {
	config, err := edgehealth.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()

	for {
		runOnce(ctx, config)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runOnce(parent context.Context, config edgehealth.Config) {
	report := edgehealth.RunChecks(parent, config)
	ctx, cancel := context.WithTimeout(parent, config.Timeout)
	defer cancel()
	if err := edgehealth.SendReport(ctx, config, report); err != nil {
		log.Printf("edge health report failed: status=%s failed=%v error=%v", report.Status, edgehealth.FailedCheckNames(report.Checks), err)
		return
	}
	log.Printf("edge health report sent: status=%s failed=%v", report.Status, edgehealth.FailedCheckNames(report.Checks))
}
