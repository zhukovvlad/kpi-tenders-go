package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
)

type Server struct {
	cfg             *config.Config
	log             *slog.Logger
	repo            *repository.Queries
	db              *pgxpool.Pool
	router          *gin.Engine
	documentService *service.DocumentService
}

func NewServer(cfg *config.Config, log *slog.Logger, pool *pgxpool.Pool) *Server {
	repo := repository.New(pool)

	s := &Server{
		cfg:             cfg,
		log:             log,
		repo:            repo,
		db:              pool,
		documentService: service.NewDocumentService(repo, log),
	}

	s.setupRouter()
	return s
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

		// Protected routes
		protected := v1.Group("")
		protected.Use(s.AuthMiddleware())
		{
			documents := protected.Group("/documents")
			{
				documents.POST("", s.CreateDocument)
				documents.GET("", s.ListDocuments)
				documents.GET("/:id", s.GetDocument)
				documents.PATCH("/:id/status", s.UpdateDocumentStatus)
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
