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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/esignoretti/ds3-sql-server/internal/analysis"
	"github.com/esignoretti/ds3-sql-server/internal/api"
	"github.com/esignoretti/ds3-sql-server/internal/auth"
	"github.com/esignoretti/ds3-sql-server/internal/cache"
	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/column"
	"github.com/esignoretti/ds3-sql-server/internal/config"
	"github.com/esignoretti/ds3-sql-server/internal/convert"
	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
	"github.com/esignoretti/ds3-sql-server/internal/query"
	"github.com/esignoretti/ds3-sql-server/internal/report"
	"github.com/esignoretti/ds3-sql-server/internal/s3"
	"github.com/esignoretti/ds3-sql-server/internal/scheduler"
	"github.com/esignoretti/ds3-sql-server/internal/web"
	"github.com/esignoretti/ds3-sql-server/internal/worker"
	"github.com/esignoretti/ds3-sql-server/internal/write"
)

// wireResolver adapts catalog.Service.Resolve to worker.WireResolver.
type wireResolver struct{ cat *catalog.Service }

// storageAdapter adapts config.Config.ResolveStorageClass → write.storageResolver.
type storageAdapter struct {
	cfg *config.Config
}

func (s storageAdapter) Resolve(class string) (string, string, bool) {
	sc, ok := s.cfg.ResolveStorageClass(class)
	if !ok {
		return "", "", false
	}
	return sc.Bucket, sc.Endpoint, true
}

// credsDeleter satisfies catalog.PrefixDeleter by creating an s3.Client with
// per-request credentials.
type credsDeleter struct {
	accessKey, secretKey, endpoint string
}

func (d *credsDeleter) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	client, err := s3.NewClient(ctx, d.accessKey, d.secretKey, d.endpoint)
	if err != nil {
		return err
	}
	return client.DeletePrefix(ctx, bucket, prefix)
}

// schedulerEnqueuer submits a schedule's SQL as a job and clears its Running
// flag when the job finishes.
type schedulerEnqueuer struct {
	mgr   *job.Manager
	store metastore.Store
}

func newSchedulerEnqueuer(mgr *job.Manager, store metastore.Store) *schedulerEnqueuer {
	return &schedulerEnqueuer{mgr: mgr, store: store}
}

func (e *schedulerEnqueuer) Enqueue(sch *metastore.Schedule) {
	typ := "query"
	if write.IsCTAS(sch.SQL) {
		typ = "ctas"
	}
	j := e.mgr.Submit(context.Background(), job.ExecRequest{
		Type:      typ,
		SQL:       sch.SQL,
		ProjectID: sch.ProjectID,
	})
	go func() {
		for {
			cur, ok := e.mgr.Get(j.ID)
			if ok && (cur.Status == "done" || cur.Status == "failed") {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		_ = e.store.UpdateScheduleRun(context.Background(), sch.ID, sch.LastRunAt, false)
		_ = e.store.SetScheduleNextRun(context.Background(), sch.ID, sch.NextRunAt)
	}()
}

func (w wireResolver) ResolveWire(ctx context.Context, projectID, sql string) ([]worker.WireBinding, error) {
	bindings, err := w.cat.Resolve(ctx, projectID, sql)
	if err != nil {
		return nil, err
	}
	out := make([]worker.WireBinding, len(bindings))
	for i, b := range bindings {
		sc := "hdd"
		if t, err := w.cat.GetTable(ctx, projectID, b.Schema, b.Name); err == nil {
			if t.StorageClass != "" {
				sc = t.StorageClass
			}
		}
		out[i] = worker.WireBinding{
			Schema:    b.Schema,
			Name:      b.Name,
			ReaderSQL: b.ReaderSQL,
			StorageClass: sc,
		}
	}
	return out, nil
}

func main() {
	port := flag.Int("port", 0, "Listening port (overrides config)")
	roleFlag := flag.String("role", "", "Node role: all (default), coordinator, worker")
	flag.Parse()

	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if *roleFlag != "" {
		cfg.Role = *roleFlag
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

	// Worker/coordinator security: shared secret is required.
	if cfg.Role == "coordinator" || cfg.Role == "worker" {
		if cfg.Cluster.SharedSecret == "" {
			log.Fatalf("cluster.shared_secret (DS3SQL_CLUSTER_SHARED_SECRET) is required for role=%q", cfg.Role)
		}
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

	// Metastore: embedded SQLite (default) or external Postgres (opt-in).
	var metaStore metastore.Store
	switch cfg.Metastore.Driver {
	case "", "sqlite":
		if dir := filepath.Dir(cfg.Metastore.Path); dir != "" {
			os.MkdirAll(dir, 0755)
		}
		s, err := metastore.OpenSQLite(cfg.Metastore.Path)
		if err != nil {
			log.Fatalf("failed to init sqlite metastore: %v", err)
		}
		metaStore = s
	case "postgres":
		if cfg.Metastore.DSN == "" {
			log.Fatalf("metastore driver 'postgres' requires metastore.dsn (DS3SQL_METASTORE_DSN)")
		}
		s, err := metastore.OpenPostgres(cfg.Metastore.DSN)
		if err != nil {
			log.Fatalf("failed to init postgres metastore: %v", err)
		}
		metaStore = s
	default:
		log.Fatalf("unknown metastore driver %q (want sqlite|postgres)", cfg.Metastore.Driver)
	}
	defer metaStore.Close()
	catService := catalog.NewService(metaStore, queryEngine)

	// Select the base executor by role.
	var baseExecutor job.Executor
	switch cfg.Role {
	case "coordinator":
		if len(cfg.Cluster.Workers) == 0 {
			log.Fatalf("role=coordinator requires cluster.workers to be configured")
		}
		baseExecutor = worker.NewRemoteExecutor(cfg.Cluster.Workers, cfg.Cluster.SharedSecret, wireResolver{cat: catService})
	default: // "all" and "worker" both run queries in-process for their own API
		baseExecutor = job.NewLocalExecutor(catService, queryEngine)
	}

	// Result cache fronts the executor (coordinator + all).
	resultCache := cache.NewResultCache(
		metaStore,
		cache.NewDirBlobstore(cfg.Cache.ResultDir),
		cache.ResultCacheOpts{TTL: cfg.Cache.ResultTTL, MaxBytes: cfg.Cache.ResultMaxBytes},
	)
	versionSource := func(ctx context.Context, projectID, sql string) (map[string]int64, error) {
		bindings, err := catService.Resolve(ctx, projectID, sql)
		if err != nil {
			return nil, err
		}
		versions := make(map[string]int64, len(bindings))
		for _, b := range bindings {
			t, err := catService.GetTable(ctx, projectID, b.Schema, b.Name)
			if err != nil {
				return nil, err
			}
			versions[projectID+"/"+b.Schema+"/"+b.Name] = t.DataVersion
		}
		return versions, nil
	}
	rawExec := func(ctx context.Context, projectID, sql, ak, sk, ep string, _ map[string]int64) *query.Result {
		return baseExecutor.Execute(ctx, job.ExecRequest{
			SQL: sql, ProjectID: projectID, AccessKey: ak, SecretKey: sk, Endpoint: ep,
		})
	}
	caching := cache.NewCachingExecutor(resultCache, rawExec, versionSource)

	// cachingExecutor makes the result cache satisfy job.Executor so the manager
	// front-runs the cache before dispatching to the base executor.
	cachingExecutor := job.ExecutorFunc(func(ctx context.Context, req job.ExecRequest) *query.Result {
		return caching.Run(ctx, req.ProjectID, req.SQL, req.AccessKey, req.SecretKey, req.Endpoint)
	})

	jobManager := job.NewManager(cachingExecutor)
	jobManager.SetSink(job.NewMetastoreSink(metaStore))
	jobManager.SetQuota(cfg.Query.MaxConcurrentPerProject)

	// Write path (CTAS, load, managed drop)
	writeWriter := write.NewWriter(queryEngine, catService, metaStore, metaStore, storageAdapter{cfg: cfg}, nil /* deleter bound per-call */)
	writeExecutor := job.NewLocalWriteExecutor(writeWriter)
	jobManager.SetWriteExecutor(writeExecutor)

	queryHandler := api.NewQueryHandler(queryEngine, catService)
	datasetHandler := api.NewDatasetHandler(catService)
	tableHandler := api.NewTableHandler(catService)
	jobHandler := api.NewJobHandler(jobManager)
	scheduleHandler := api.NewScheduleHandler(metaStore)
	catalogFragmentHandler := api.NewCatalogFragmentHandler(catService)

	// Worker data-plane server (role=worker): exposes /internal/execute guarded
	// by the shared secret, fronted by a local-SSD data cache.
	if cfg.Role == "worker" {
		var dataCache *cache.DataCache
		if cfg.Cache.DataDir != "" && cfg.Cache.DataMaxBytes > 0 {
			dataCache = nil
		}
		workerSrv := worker.NewServer(queryEngine, cfg.Cluster.SharedSecret, dataCache)
		r.Group(func(r chi.Router) {
			r.Post("/internal/execute", workerSrv.Execute)
		})
	}

	// Periodic job cleanup
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			jobManager.Cleanup(30 * time.Minute)
		}
	}()

	// Scheduler runs only on the control plane (coordinator/all).
	if cfg.Role == "coordinator" || cfg.Role == "all" {
		schedEnqueuer := newSchedulerEnqueuer(jobManager, metaStore)
		sched := scheduler.New(metaStore, schedEnqueuer)
		schedCtx, schedCancel := context.WithCancel(context.Background())
		defer schedCancel()
		go sched.Run(schedCtx, 30*time.Second)
	}

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

		// Dataset routes
		r.Post("/datasets", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					datasetHandler.CreateForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Delete("/datasets/{dataset}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					datasetHandler.DeleteForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Get("/datasets", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					datasetHandler.ListForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})

		// Table routes
		r.Post("/datasets/{dataset}/tables", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					tableHandler.RegisterForProject(w, r, p.ProjectID, p.AccessKey, p.SecretKey, session.GatewayEndpoint)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Get("/datasets/{dataset}/tables", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					tableHandler.ListForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Get("/datasets/{dataset}/tables/{table}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					tableHandler.DescribeForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Delete("/datasets/{dataset}/tables/{table}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					deleter := &credsDeleter{accessKey: p.AccessKey, secretKey: p.SecretKey, endpoint: session.GatewayEndpoint}
					tableHandler.DropWithDeps(w, r, p.ProjectID, deleter, metaStore, p.AccessKey, p.SecretKey, session.GatewayEndpoint)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})

		// Catalog tree fragment (server-rendered HTML for the Web UI)
		r.Get("/ui/catalog", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					catalogFragmentHandler.TreeForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})

		// Job routes
		r.Get("/jobs", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					jobHandler.ListForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Post("/jobs", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					jobHandler.SubmitWithCreds(w, r, p.ProjectID, p.AccessKey, p.SecretKey, session.GatewayEndpoint)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})

		// Schedule routes
		r.Post("/schedules", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					scheduleHandler.CreateForProject(w, r, p.ProjectID, session.Email)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Get("/schedules", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					scheduleHandler.ListForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"select a project first"}`, http.StatusBadRequest)
		})
		r.Delete("/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					scheduleHandler.DeleteForProject(w, r, p.ProjectID)
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
		r.Get("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					jobHandler.GetForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		})
		r.Delete("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
			session := auth.GetSession(r)
			projectID := r.URL.Query().Get("project")
			for _, p := range session.Projects {
				if projectID == "" || p.ProjectID == projectID {
					jobHandler.CancelForProject(w, r, p.ProjectID)
					return
				}
			}
			http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		})
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
		r.Get("/app", webHandler.AppPage)
		r.Get("/reports", webHandler.ReportsPage)
		// Redirect old routes to new app
		r.Get("/browse", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/app", http.StatusFound)
		})
		r.Get("/query", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/app", http.StatusFound)
		})
		r.Get("/report", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/app#report", http.StatusFound)
		})
		r.Get("/column-config", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/app#transform", http.StatusFound)
		})
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
