package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kevinpita/gasolinerabot/internal/bot"
)

var version = "dev"

func main() {
	if e := run(); e != nil {
		slog.Error("bot stopped", "error", e.Error())
		os.Exit(1)
	}
}
func run() error {
	show := flag.Bool("version", false, "Print version")
	importPrefs := flag.Bool("import-preferences", false, "Import private JSON preferences from stdin, then exit")
	flag.Parse()
	if *show {
		fmt.Println(version)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	url := os.Getenv("DATABASE_URL")
	if url == "" && os.Getenv("PGHOST") == "" {
		return errors.New("DATABASE_URL or PostgreSQL environment variables are required")
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	store, e := bot.OpenStore(startup, url)
	cancel()
	if e != nil {
		return errors.New("database startup failed")
	}
	defer store.Pool.Close()
	if *importPrefs {
		n, e := store.ImportPrefs(ctx, os.Stdin)
		if e != nil {
			return errors.New("preferences import failed, transaction rolled back")
		}
		fmt.Printf("Imported %d preferences. Existing chats were not changed.\n", n)
		return nil
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 40 * time.Second
	client := &http.Client{Timeout: 45 * time.Second, Transport: transport}
	shortClient := &http.Client{Timeout: 12 * time.Second, Transport: transport}
	telegram := &bot.Telegram{HTTP: client, Base: "https://api.telegram.org/bot" + token}
	startup, cancel = context.WithTimeout(ctx, 20*time.Second)
	e = telegram.Call(startup, "getMe", map[string]any{}, nil)
	cancel()
	if e != nil {
		return errors.New("Telegram authentication failed")
	}
	app := &bot.App{Store: store, Telegram: telegram, Prices: &bot.PriceSource{HTTP: client, Store: store}, Routing: &bot.Routing{HTTP: shortClient}, Maps: &bot.Maps{HTTP: shortClient}, Log: slog.Default()}
	addr := os.Getenv("HEALTH_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	listener, e := net.Listen("tcp", addr)
	if e != nil {
		return errors.New("health listener failed")
	}
	var ready atomic.Bool
	ready.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if store.Pool.Ping(c) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if e := server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			slog.Error("health server failed")
			stop()
		}
	}()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c, cancel := context.WithTimeout(ctx, 5*time.Minute)
				if e := app.CheckAlerts(c); e != nil && ctx.Err() == nil {
					slog.Error("alert check failed")
				}
				cancel()
			}
		}
	}()
	e = app.Run(ctx)
	ready.Store(false)
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	wg.Wait()
	if errors.Is(e, context.Canceled) {
		return nil
	}
	return e
}
