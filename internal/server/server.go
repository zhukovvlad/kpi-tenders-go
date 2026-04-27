package server

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/internal/storage"
	"go-kpi-tenders/internal/store"
)

type Server struct {
	cfg                     *config.Config
	log                     *slog.Logger
	store                   store.Store
	storageClient           storageClient // nil when S3 not configured
	router                  *gin.Engine
	pythonClient            *pythonworker.Publisher // closed on server shutdown
	authService             *service.AuthService
	organizationService     *service.OrganizationService
	userService             *service.UserService
	constructionSiteService *service.ConstructionSiteService
	documentService         *service.DocumentService
	documentTaskService     *service.DocumentTaskService
	workerService           *service.WorkerService
	extractionService       *service.ExtractionService
}

func NewServer(cfg *config.Config, log *slog.Logger, pool *pgxpool.Pool) (*Server, error) {
	// pool may be nil in unit tests that only exercise routing/middleware.
	// In that case services receive a nil querier/store, which is safe as long
	// as no handler that reaches the service layer is called in those tests.
	// In production pool is always non-nil (enforced by cmd/api/main.go).
	var db store.Store
	if pool != nil {
		db = store.New(pool)
	}

	// storageClient may be nil if S3 credentials are not configured (e.g. some
	// unit-test scenarios). Upload endpoints will return 500 in that case.
	var sc *storage.Client
	hasAccessKey := cfg.S3AccessKey != ""
	hasSecretKey := cfg.S3SecretKey != ""
	switch {
	case hasAccessKey && hasSecretKey:
		var err error
		sc, err = storage.New(cfg)
		if err != nil {
			// Non-fatal: server starts, upload endpoints degrade gracefully.
			log.Error("storage: failed to init MinIO client", "err", err)
		}
	case hasAccessKey || hasSecretKey:
		// Misconfiguration: only one of the two credentials is set.
		log.Error("storage: S3AccessKey and S3SecretKey must both be set",
			"hasAccessKey", hasAccessKey,
			"hasSecretKey", hasSecretKey,
		)
	}

	// docStorage is a nil interface when S3 is not configured, ensuring
	// NewDocumentService receives a true interface nil (not a typed nil pointer)
	// so that nil checks inside the service work correctly.
	var docStorage storageClient
	if sc != nil {
		docStorage = sc
	}

	srv := &Server{
		cfg:                     cfg,
		log:                     log,
		store:                   db,
		authService:             service.NewAuthService(db, log, cfg.JWTAccessSecret, cfg.JWTRefreshSecret),
		organizationService:     service.NewOrganizationService(db, log),
		userService:             service.NewUserService(db, log),
		constructionSiteService: service.NewConstructionSiteService(db, log),
		documentService:         service.NewDocumentService(db, docStorage, log),
	}

	// pythonClient publishes Celery tasks directly to Redis, shared by both
	// documentTaskService (initial trigger) and workerService (chained tasks).
	// Redis is mandatory — invalid URL is a misconfiguration, caller must handle.
	pythonClient, err := pythonworker.New(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("server: init redis publisher: %w", err)
	}

	srv.pythonClient = pythonClient
	srv.documentTaskService = service.NewDocumentTaskService(db, pythonClient, log)
	srv.workerService = service.NewWorkerService(db, pythonClient, log)
	srv.extractionService = service.NewExtractionService(db, pythonClient, log)
	if sc != nil {
		// storageClient is set after struct creation to avoid storing a
		// (*storage.Client)(nil) as a non-nil interface value.
		srv.storageClient = sc
	}

	srv.setupRouter()
	return srv, nil
}

// Close releases resources held by the server (Redis connection pool).
// Call this during graceful shutdown after the HTTP server has drained.
func (s *Server) Close() error {
	if s == nil || s.pythonClient == nil {
		return nil
	}
	return s.pythonClient.Close()
}

// PythonPublisher returns the Redis/Celery publisher used by the server.
// Initialised as part of server construction; always non-nil after NewServer succeeds.
func (s *Server) PythonPublisher() *pythonworker.Publisher {
	return s.pythonClient
}

// DB returns the store.Store (implements repository.Querier) used by the server.
// It is nil when the server was initialised without a database (unit tests).
func (s *Server) DB() store.Store {
	return s.store
}

func (s *Server) setupRouter() {
	if s.cfg.AppEnv != "local" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.loggerMiddleware())
	r.Use(s.corsMiddleware())

	// ── Health ──────────────────────────────────────
	r.GET("/health", s.HealthCheck)

	// ── Internal (service-to-service) ───────────────
	internal := r.Group("/internal/worker")
	internal.Use(s.ServiceBearerAuth())
	{
		internal.PATCH("/tasks/:id/status", s.WorkerUpdateTaskStatus)
	}

	// ── Public API v1 ───────────────────────────────
	v1 := r.Group("/api/v1")
	{
		// Public routes (no auth)
		v1.GET("/health", s.HealthCheck)

		// Auth routes
		auth := v1.Group("/auth")
		{
			auth.POST("/register", s.RegisterOrganization)
			auth.POST("/login", s.Login)
			auth.POST("/refresh", s.RefreshTokens)
			auth.POST("/logout", s.Logout)
			auth.GET("/me", s.AuthMiddleware(), s.GetMe)
		}

		// Protected routes
		protected := v1.Group("")
		protected.Use(s.AuthMiddleware())
		{
			organizations := protected.Group("/organizations")
			{
				organizations.GET("/:id", s.GetOrganization)
				organizations.PATCH("/:id", s.UpdateOrganization)
				organizations.DELETE("/:id", s.DeleteOrganization)
			}

			sites := protected.Group("/sites")
			{
				sites.POST("", s.CreateConstructionSite)
				sites.GET("", s.ListConstructionSites)
				sites.GET("/:id", s.GetConstructionSite)
				sites.PATCH("/:id", s.UpdateConstructionSite)
				sites.DELETE("/:id", s.DeleteConstructionSite)
			}

			documents := protected.Group("/documents")
			{
				documents.POST("", s.CreateDocument)
				documents.POST("/upload", s.UploadDocument)
				documents.GET("", s.ListDocuments)
				documents.GET("/:id", s.GetDocument)
				documents.GET("/:id/url", s.GetDocumentPresignedURL)
				documents.DELETE("/:id", s.DeleteDocument)
				documents.POST("/:id/extract", s.InitiateExtraction)
			}

			tasks := protected.Group("/tasks")
			{
				tasks.POST("", s.CreateDocumentTask)
				tasks.GET("", s.ListDocumentTasks)
				tasks.GET("/:id", s.GetDocumentTask)
				tasks.PATCH("/:id/status", s.UpdateDocumentTaskStatus)
				tasks.DELETE("/:id", s.DeleteDocumentTask)
			}

			users := protected.Group("/users")
			users.Use(s.AdminOnly())
			{
				users.POST("", s.CreateUser)
				users.GET("", s.ListUsers)
				users.PATCH("/:id", s.UpdateUser)
				users.DELETE("/:id", s.DeactivateUser)
			}
		}
	}

	s.router = r
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	cfg := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	if s.cfg.AppEnv == "local" {
		cfg.AllowAllOrigins = true
	} else {
		cfg.AllowOrigins = []string{"https://*.kpi-tenders.kz"}
	}

	return cors.New(cfg)
}

func (s *Server) loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		s.log.Info("request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
		)
	}
}

func (s *Server) Start(address string) error {
	return s.router.Run(address)
}

// Router exposes the engine for testing.
func (s *Server) Router() *gin.Engine {
	return s.router
}
