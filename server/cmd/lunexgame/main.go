package main

import (
	"log"
	"net"
	"net/http"
	"time"

	"lunexgame/internal/app"
)

// Задаются через -ldflags при сборке.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

// rollbackTest — ВРЕМЕННО, для проверки отката деплоя; будет откачено git revert.
const rollbackTest = true

func main() {
	if rollbackTest {
		log.Fatal("ROLLBACK-TEST: намеренный сбой запуска")
	}
	addr, cfg := app.FromEnv()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		log.Fatalf("LUNEXGAME_LISTEN: %v", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		log.Fatalf("LUNEXGAME_LISTEN должен быть loopback-адресом, получено %q", addr)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.New(app.Build{Version: version, Commit: commit, BuildTime: buildTime}, cfg),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("lunexgame %s (%s) слушает %s", version, commit, addr)
	log.Fatal(srv.ListenAndServe())
}
