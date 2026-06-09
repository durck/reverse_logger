package main

import (
	"log"
	"net"

	"github.com/durck/reverse_logger/internal/edgeapp"
	"github.com/durck/reverse_logger/internal/store"
)

func main() {
	config, err := edgeapp.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	listener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Printf("edge-logger listening on %s and proxying to %s", config.ListenAddr, config.TargetAddr)
	if err := edgeapp.NewProxy(config, st).Serve(listener); err != nil {
		log.Fatal(err)
	}
}
