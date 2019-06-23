package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/devopyio/config-reloader/pkg/reloader"
	"github.com/povilasv/prommod"
	"github.com/prometheus/client_golang/prometheus"
	ver "github.com/prometheus/common/version"
	log "github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

func main() {
	app := kingpin.New("config-reloader", "Reloads Configuration")

	app.Version(ver.Print("config-reloader"))
	app.HelpFlag.Short('h')
	prometheus.MustRegister(ver.NewCollector("zal"))
	prometheus.MustRegister(prommod.NewCollector("zal"))

	cfgFile := app.Flag("config", "config file or directory watched by the reloader").
		Envar("CONFIG").Required().String()

	logLevel := app.Flag("log.level", "Log level.").
		Default("info").Enum("error", "warn", "info", "debug")
	logFormat := app.Flag("log.format", "Log format.").
		Default("text").Enum("text", "json")

	reloadURL := app.Flag("webhook-url", "webook URL to send HTTP GET on").
		Envar("RELOAD_URL").Required().URL()

	watchInterval := app.Flag("watch-interval", "watch interval").
		Envar("WATCH_INTERVAL").Default("3m").Duration()

	kingpin.MustParse(app.Parse(os.Args[1:]))

	switch strings.ToLower(*logLevel) {
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	}

	switch strings.ToLower(*logFormat) {
	case "json":
		log.SetFormatter(&log.JSONFormatter{})
	case "text":
		log.SetFormatter(&log.TextFormatter{DisableColors: true})
	}
	log.SetOutput(os.Stdout)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("Starting config-reloader version.", ver.Version)

	rel := reloader.New(*reloadURL, *cfgFile, *watchInterval)
	go func() {
		if err := rel.Watch(ctx); err != nil {
			log.WithError(err).Panic("error watching config")
		}
	}()

	waitForInterrupt()
}

func waitForInterrupt() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Warn("shutting down")
}
