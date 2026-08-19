package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"messenger/internal/server"
	"messenger/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dsn := env("DB_DSN", "postgres://messenger:messenger@localhost:5432/messenger")
	addr := env("ADDR", ":8443")
	certFile := env("TLS_CERT", "certs/server.crt")
	keyFile := env("TLS_KEY", "certs/server.key")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := store.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer s.Close()

	if err := s.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	crypt, err := server.NewCryptor(env("MASTER_KEY", ""))
	if err != nil {
		log.Fatalf("cryptor: %v", err)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: server.New(s, crypt).Handler(),
	}

	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := s.CleanupExpired(ctx); err != nil {
					log.Printf("cleanup: %v", err)
				} else if n > 0 {
					log.Printf("cleanup: removed %d expired sessions", n)
				}
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("messenger server listening on %s", addr)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
