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
	"github.com/esignoretti/ds3-sql-server/internal/analysis"
	"github.com/esignoretti/ds3-sql-server/internal/api"
	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/column"
	"github.com/esignoretti/ds3-sql-server/internal/config"
	"github.com/esignoretti/ds3-sql-server/internal/convert"
	"github.com/esignoretti/ds3-sql-server/internal/query"
	"github.com/esignoretti/ds3-sql-server/internal/report"
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

	// Auth
	iamClient := auth.NewIAMClient(cfg.IAMURL)
	sessionStore := auth.NewSessionStore()
	authHandler := api.NewAuthHandler(iamClient, sessionStore)

	r.Post("/auth/login", authHandler.Login)
	r.Post("/auth/refresh", authHandler.Refresh)
	r.Get("/auth/logout", authHandler.Logout)

	// Query engine
	queryEngine, err := query.NewEngine(
		cfg.Query.MaxRows,
		cfg.Query.MaxExecutionSecs,
		cfg.Query.MaxResultBytes,
		cfg.Query.PoolSize,
		cfg.Query.Threads,
		cfg.Query.MemoryLimit,
	)
	if err != nil {
		log.Fatalf("failed to init query engine: %v", err)
	}
	queryHandler := api.NewQueryHandler(queryEngine)
	schemaHandler := api.NewSchemaHandler(queryEngine)

	// Analysis engine
	analysisEngine := analysis.NewEngine(queryEngine.Pool())
	analysisHandler := api.NewAnalysisHandler(analysisEngine)

	// Report store
	reportDir := os.Getenv("DS3SQL_REPORT_DIR")
	if reportDir == "" {
		home, _ := os.UserHomeDir()
		reportDir = home + "/.ds3sql/reports"
	}
	reportStore, err := report.NewDiskStore(reportDir)
	if err != nil {
		log.Fatalf("failed to init report store: %v", err)
	}
	reportHandler := api.NewReportHandler(reportStore)

	// Column config store
	columnDir := os.Getenv("DS3SQL_COLUMN_DIR")
	if columnDir == "" {
		home, _ := os.UserHomeDir()
		columnDir = home + "/.ds3sql/columns"
	}
	columnStore, err := column.NewStore(columnDir)
	if err != nil {
		log.Fatalf("failed to init column store: %v", err)
	}
	columnHandler := api.NewColumnHandler(columnStore)

	// Conversion engine
	workers := 4
	if cfg.Query.PoolSize < workers {
		workers = cfg.Query.PoolSize
	}
	convertEngine := convert.NewEngine(queryEngine.Pool(), workers, columnStore)
	convertHandler := api.NewConvertHandler(convertEngine)

	// Periodic job store cleanup (runs until process exits)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			convertEngine.JobStore().Cleanup(30 * time.Minute)
		}
	}()

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		poolLen := queryEngine.PoolLen()
		if poolLen > 0 {
			w.Write([]byte(`{"status":"ok","pool_size":` + strconv.Itoa(poolLen) + `}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"degraded","error":"query pool empty"}`))
		}
	})

	// Protected group (with 60s timeout for query endpoints)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(sessionStore))
		r.Use(middleware.Timeout(60 * time.Second))
		r.Get("/auth/me", authHandler.Me)

			s3ClientForProject := func(r *http.Request) *s3.Client {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					client, err := s3.NewClient(r.Context(), p.AccessKey, p.SecretKey, session.GatewayEndpoint)
					if err != nil {
						return nil
					}
					return client
				}
			}
			return nil
		}

		r.Get("/buckets", func(w http.ResponseWriter, r *http.Request) {
			client := s3ClientForProject(r)
			if client == nil {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			api.NewBucketHandler(client).ListBuckets(w, r)
		})

		r.Get("/buckets/{bucket}", func(w http.ResponseWriter, r *http.Request) {
			client := s3ClientForProject(r)
			if client == nil {
				http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
				return
			}
			api.NewBucketHandler(client).ListObjects(w, r)
		})

		r.Post("/query", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					queryHandler.QueryWithCreds(w, r, p.AccessKey, p.SecretKey, session.GatewayEndpoint)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})

		r.Post("/schema", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					schemaHandler.InferSchemaWithCreds(w, r, p.AccessKey, p.SecretKey, session.GatewayEndpoint)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
	})

	// Protected group (no timeout — long-running operations)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(sessionStore))
		r.Post("/analyze", analysisHandler.Analyze)
		r.Get("/api/reports", reportHandler.List)
		r.Post("/api/reports", reportHandler.Save)
		r.Get("/api/reports/{id}", reportHandler.Get)
		r.Delete("/api/reports/{id}", reportHandler.Delete)
		r.Post("/convert", convertHandler.Start)
		r.Get("/convert/status/{id}", convertHandler.Status)
		r.Get("/convert/preview", columnHandler.Preview)
		r.Get("/convert/columns", columnHandler.ListConfigs)
		r.Post("/convert/columns", columnHandler.SaveConfig)
		r.Delete("/convert/columns/{bucket}/{pattern}", columnHandler.DeleteConfig)
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
		r.Get("/report", webHandler.ReportPage)
		r.Get("/reports", webHandler.ReportsPage)
		r.Get("/column-config", webHandler.ColumnConfigPage)
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
