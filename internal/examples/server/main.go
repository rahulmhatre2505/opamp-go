package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/open-telemetry/opamp-go/internal/examples/server/data"
	"github.com/open-telemetry/opamp-go/internal/examples/server/opampsrv"
	"github.com/open-telemetry/opamp-go/internal/examples/server/uisrv"
)

var logger = log.New(log.Default().Writer(), "[MAIN] ", log.Default().Flags()|log.Lmsgprefix|log.Lmicroseconds)

func main() {
	var emitMetrics bool
	flag.BoolVar(&emitMetrics, "emit-metrics", false, "Emit metrics to stdout.")

	flag.Parse()

	curDir, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	logger.Println("OpAMP Server starting...")

	if err = data.InitCustomConfigStore(filepath.Join(curDir, "custom_configs.json")); err != nil {
		logger.Printf("Cannot initialize custom config store: %v", err)
	}

	uisrv.Start(curDir)
	opampSrv := opampsrv.NewServer(&data.AllAgents, emitMetrics)
	opampSrv.Start()

	logger.Println("OpAMP Server running...")

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	logger.Println("OpAMP Server shutting down...")
	uisrv.Shutdown()
	opampSrv.Stop()
}
