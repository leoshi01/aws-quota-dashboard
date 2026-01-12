package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuxishi/aws-quota-dashboard/internal/aws"
	"github.com/yuxishi/aws-quota-dashboard/internal/cache"
	"github.com/yuxishi/aws-quota-dashboard/internal/handler"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cacheTTL := 5 * time.Minute
	c := cache.New(cacheTTL)
	fetcher := aws.NewQuotaFetcher(10)
	h := handler.New(fetcher, c)

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
