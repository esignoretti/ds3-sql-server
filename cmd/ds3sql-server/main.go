package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/api"
	"github.com/esignoretti/ds3-sql-server/internal/config"
	"github.com/esignoretti/ds3-sql-server/internal/query"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
	"github.com/esignoretti/ds3-sql-server/internal/web"
)

func main() {
	port := flag.Int("port", 0, "Listening port (overrides config)")
	flag.Parse()

	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if *port != 0 {
		host := ""
		if addr := cfg.ListenAddr; addr != "" {
			if i := strings.LastIndex(addr, ":"); i >= 0 {
				host = addr[:i]
			}
		}
		cfg.ListenAddr = host + ":" + strconv.Itoa(*port)
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Auth
	iamClient := auth.NewIAMClient(cfg.IAMURL)
	sessionStore := auth.NewSessionStore()
	authHandler := api.NewAuthHandler(iamClient, sessionStore)

	r.Post("/auth/login", authHandler.Login)
	r.Post("/auth/refresh", authHandler.Refresh)
	r.Get("/auth/logout", authHandler.Logout)

	// Query engine
	queryEngine := query.NewEngine(
		cfg.Query.MaxRows,
		cfg.Query.MaxExecutionSecs,
		cfg.Query.MaxResultBytes,
	)
	queryHandler := api.NewQueryHandler(queryEngine)
	schemaHandler := api.NewSchemaHandler(queryEngine)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Protected group
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(sessionStore))
		r.Get("/auth/me", authHandler.Me)

		// S3 bucket routes
		r.Get("/buckets", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			client, err := s3.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
			if err != nil {
				http.Error(w, `{"error":"failed to init s3 client"}`, http.StatusInternalServerError)
				return
			}
			if r.Header.Get("HX-Request") == "true" {
				api.NewBucketHandler(client).ListBucketsHTML(w, r)
				return
			}
			api.NewBucketHandler(client).ListBuckets(w, r)
		})

		r.Get("/buckets/{bucket}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			client, err := s3.NewClient(r.Context(), session.AccessKey, session.SecretKey, session.GatewayEndpoint)
			if err != nil {
				http.Error(w, `{"error":"failed to init s3 client"}`, http.StatusInternalServerError)
				return
			}
			api.NewBucketHandler(client).ListObjects(w, r)
		})

		r.Post("/query", queryHandler.Query)
		r.Post("/schema", schemaHandler.InferSchema)
	})

	// Web UI
	webHandler, err := web.NewHandler()
	if err != nil {
		log.Fatalf("failed to init web handler: %v", err)
	}

	// Public pages
	r.Get("/login", webHandler.LoginPage)
	r.Handle("/static/*", webHandler.Static())
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	// Protected pages
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(sessionStore))
		r.Get("/browse", webHandler.BrowsePage)
		r.Get("/query", webHandler.QueryPage)
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Printf("DS3 SQL Server listening on %s\n", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-done
	fmt.Println("\nshutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}
