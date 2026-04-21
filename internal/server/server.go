package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/internal/store"
)

type Server struct {
	cfg                     *config.Config
	log                     *slog.Logger
	store                   store.Store
	router                  *gin.Engine
	authService             *service.AuthService
	organizationService     *service.OrganizationService
	userService             *service.UserService
	constructionSiteService *service.ConstructionSiteService
	documentService         *service.DocumentService
	documentTaskService     *service.DocumentTaskService
}

func NewServer(cfg *config.Config, log *slog.Logger, pool *pgxpool.Pool) *Server {
	// pool may be nil in unit tests that only exercise routing/middleware.
	// In that case services receive a nil querier/store, which is safe as long
	// as no handler that reaches the service layer is called in those tests.
	// In production pool is always non-nil (enforced by cmd/api/main.go).
	var db store.Store
	if pool != nil {
		db = store.New(pool)
	}

	srv := &Server{
		cfg:                     cfg,
		log:                     log,
		store:                   db,
		authService:             service.NewAuthService(db, log, cfg.JWTAccessSecret, cfg.JWTRefreshSecret),
		organizationService:     service.NewOrganizationService(db, log),
		userService:             service.NewUserService(db, log),
		constructionSiteService: service.NewConstructionSiteService(db, log),
		documentService:         service.NewDocumentService(db, log),
		documentTaskService:     service.NewDocumentTaskService(db, log),
	}

	srv.setupRouter()
	return srv
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
		// TODO: register Python worker endpoints
		internal.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "pong"})
		})
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
				documents.GET("", s.ListDocuments)
				documents.GET("/:id", s.GetDocument)
				documents.DELETE("/:id", s.DeleteDocument)
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
				users.PATCH("/:user_id", s.UpdateUser)
				users.DELETE("/:user_id", s.DeactivateUser)
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
