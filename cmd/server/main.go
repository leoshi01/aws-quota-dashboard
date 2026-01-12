package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/yuxishi/aws-quota-dashboard/internal/aws"
	"github.com/yuxishi/aws-quota-dashboard/internal/cache"
	"github.com/yuxishi/aws-quota-dashboard/internal/config"
	"github.com/yuxishi/aws-quota-dashboard/internal/handler"
)

func main() {
	// Load configuration
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Printf("Warning: failed to load config.yaml, using defaults: %v", err)
		cfg = config.Default()
	}
	log.Printf("Configuration loaded: default_region=%s, default_service=%s", cfg.DefaultRegion, cfg.DefaultService)

	port := cfg.GetPort()
	cacheTTL := cfg.GetCacheTTL()
	c := cache.New(cacheTTL)
	fetcher := aws.NewQuotaFetcher(cfg.MaxConcurrency)
	h := handler.New(fetcher, c)

	// Set config for API access
	h.SetConfig(map[string]interface{}{
		"default_region":  cfg.DefaultRegion,
		"default_service": cfg.DefaultService,
	})

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Find templates directory
	templateDir := findTemplateDir()
	r.LoadHTMLGlob(filepath.Join(templateDir, "*.html"))

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	api := r.Group("/api")
	{
		api.GET("/config", h.GetConfig)
		api.GET("/regions", h.GetRegions)
		api.GET("/services", h.GetServices)
		api.GET("/quotas", h.GetQuotas)
		api.POST("/refresh", h.Refresh)
		api.GET("/export/json", h.ExportJSON)
		api.GET("/export/html", h.ExportHTML)
	}

	log.Printf("Starting server on http://localhost:%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func findTemplateDir() string {
	// Check common locations
	paths := []string{
		"web/templates",
		"../../web/templates",
		"../web/templates",
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Default
	return "web/templates"
}
