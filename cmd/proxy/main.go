// Command proxy is the easy-docker-proxy entrypoint.
// M1–M4: data plane, pull records, stats UI, deploy hardening metrics.
// See .ai/01_DESIGN.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/admin"
	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/metrics"
	"github.com/alex_wuyh/easy-docker-proxy/internal/proxy"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
	"github.com/alex_wuyh/easy-docker-proxy/internal/statsapi"
	"github.com/alex_wuyh/easy-docker-proxy/internal/store"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config YAML")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	token := config.AdminToken(cfg)
	if token == "" {
		log.Printf("warning: %s unset — admin API (except /healthz) is fail-closed", cfg.Admin.TokenEnv)
	}

	// Pull event store (M2) + web users
	st, err := store.Open(cfg.Storage)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Printf("store close: %v", err)
		}
	}()

	// Bootstrap first web admin from env (required once if no users)
	webUser := os.Getenv("PROXY_WEB_USER")
	if webUser == "" {
		webUser = "admin"
	}
	webPass := os.Getenv("PROXY_WEB_PASSWORD")
	if created, err := st.BootstrapAdmin(context.Background(), webUser, webPass); err != nil {
		if webPass == "" {
			n, _ := st.CountUsers(context.Background())
			if n == 0 {
				log.Printf("warning: no web users — set PROXY_WEB_PASSWORD (and optional PROXY_WEB_USER) to create admin, then restart")
			}
		} else {
			log.Printf("warning: bootstrap web admin: %v", err)
		}
	} else if created {
		log.Printf("created initial web admin user %q (change password after login)", webUser)
	}
	_ = st.PurgeExpiredSessions(context.Background())

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	st.StartRetentionLoop(bgCtx, time.Hour)

	queue := record.NewQueue(st, record.Options{})
	queue.Start()
	defer queue.Close()

	mc := metrics.New()
	p := proxy.New(cfg)
	p.SetEmitter(queue)
	p.SetMetrics(mc)

	reload := func() error {
		next, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		// Storage DSN is not re-opened on reload (restart to change DB path).
		p.Reload(next)
		return nil
	}

	// Data plane
	dataSrv := &http.Server{
		Addr:        cfg.Server.Listen,
		Handler:     p,
		ReadTimeout: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		// WriteTimeout 0: unlimited streaming for large blobs.
		IdleTimeout: time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}
	if cfg.Server.WriteTimeout > 0 {
		dataSrv.WriteTimeout = time.Duration(cfg.Server.WriteTimeout) * time.Second
	}

	// Admin plane + Stats + optional metrics
	ah := &admin.Handler{
		Proxy:      p,
		ConfigPath: *configPath,
		ReloadFunc: reload,
		Stats:      &statsapi.API{Store: st},
		Store:      st,
	}
	if cfg.Metrics.Enabled {
		ah.MetricsHandler = mc.Handler(func() {
			mc.SetEventStats(queue.Written(), queue.Dropped())
		})
	}
	adminHandler := admin.NewMux(ah)
	adminSrv := &http.Server{
		Addr:         cfg.Server.AdminListen,
		Handler:      adminHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("registry proxy listening on %s", cfg.Server.Listen)
		if err := dataSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("data plane: %w", err)
		}
	}()
	go func() {
		log.Printf("admin API listening on %s", cfg.Server.AdminListen)
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("admin plane: %w", err)
		}
	}()
	log.Printf("pull events: sqlite (%s)", cfg.Storage.DSN)
	log.Printf("stats UI: http://%s/stats/login.html (web login required)", cfg.Server.AdminListen)
	if cfg.Metrics.Enabled {
		log.Printf("metrics: http://%s/metrics (auth required)", cfg.Server.AdminListen)
	}

	// SIGHUP → reload; SIGINT/SIGTERM → graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case err := <-errCh:
			log.Fatalf("%v", err)
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				log.Printf("SIGHUP: reloading config from %s", *configPath)
				if err := reload(); err != nil {
					log.Printf("reload error: %v", err)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				log.Printf("shutting down (%v)...", sig)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_ = dataSrv.Shutdown(ctx)
				_ = adminSrv.Shutdown(ctx)
				cancel()
				bgCancel()
				if d := queue.Dropped(); d > 0 {
					log.Printf("events dropped (buffer full): %d", d)
				}
				return
			}
		}
	}
}
