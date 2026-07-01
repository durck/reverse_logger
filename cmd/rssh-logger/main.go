package main

import (
	"log"
	"net/http"
	"time"

	"github.com/durck/reverse_logger/internal/loggerapp"
	"github.com/durck/reverse_logger/internal/store"
	"github.com/durck/reverse_logger/internal/telegram"
)

func main() {
	config, err := loggerapp.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	st.SetCorrelationConfig(config.Correlation)
	defer st.Close()

	tg, err := telegram.New(config.Telegram)
	if err != nil {
		log.Fatal(err)
	}

	server := loggerapp.NewServerWithDashboardToken(config.WebhookToken, config.EdgeForwardToken, st, tg, config.IngressWSPath, config.IngressPushPath, config.DashboardToken)
	log.Printf("rssh-logger listening on %s", config.ListenAddr)
	httpServer := &http.Server{
		Addr:              config.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
