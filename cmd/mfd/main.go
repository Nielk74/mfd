package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Nielk74/mfd/internal/etoro"
	"github.com/Nielk74/mfd/internal/httpapi"
	"github.com/Nielk74/mfd/internal/platform"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		r, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("mfd stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if env("MFD_MODE", "fixture") != "fixture" {
		return fmt.Errorf("only fixture mode is implemented")
	}
	workers, err := strconv.Atoi(env("MFD_WORKERS", "2"))
	if err != nil || workers < 1 || workers > 16 {
		return fmt.Errorf("MFD_WORKERS must be 1..16")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	p, err := platform.Open(startup, env("DATABASE_URL", "postgres://mfd:mfd-local@127.0.0.1:5432/mfd?sslmode=disable"), env("REDIS_ADDR", "127.0.0.1:6379"), env("NATS_URL", "nats://127.0.0.1:4222"))
	cancel()
	if err != nil {
		return err
	}
	defer p.Close()
	apiKey := os.Getenv("ETORO_API_KEY")
	userKeys := map[string]string{"demo": os.Getenv("ETORO_DEMO_USER_KEY"), "real": os.Getenv("ETORO_REAL_USER_KEY")}
	if apiKey != "" || userKeys["demo"] != "" || userKeys["real"] != "" {
		if apiKey == "" || (userKeys["demo"] == "" && userKeys["real"] == "") {
			return fmt.Errorf("eToro application key and at least one user key are required")
		}
		clients := map[string]*etoro.Client{}
		for environment, userKey := range userKeys {
			if userKey != "" {
				clients[environment] = &etoro.Client{APIKey: apiKey, UserKey: userKey}
			}
		}
		p.Broker, err = platform.NewBrokerService(p.DB, clients, os.Getenv("MFD_ACCOUNT_ENCRYPTION_KEY"), os.Getenv("MFD_OPERATOR_TOKEN"))
		if err != nil {
			return err
		}
	}
	wait, err := p.Start(ctx, workers)
	if err != nil {
		return err
	}
	defer wait()
	defer stop()
	var brokerWorkers sync.WaitGroup
	if p.Broker != nil {
		brokerWorkers.Add(1)
		go func() { defer brokerWorkers.Done(); p.Broker.Run(ctx) }()
	}
	defer func() { stop(); brokerWorkers.Wait() }()
	server := &http.Server{Addr: env("MFD_ADDR", "0.0.0.0:8080"), Handler: httpapi.New(p), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() {
		slog.Info("lab ready", "address", server.Addr, "mode", "fixture", "workers", workers)
		done <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	return server.Shutdown(shutdown)
}
