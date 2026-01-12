package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuxishi/aws-quota-dashboard/internal/aws"
	"github.com/yuxishi/aws-quota-dashboard/internal/cache"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
)

type Handler struct {
	fetcher *aws.QuotaFetcher
	cache   *cache.Cache
	config  interface{} // Store config for API access
}

func New(fetcher *aws.QuotaFetcher, cache *cache.Cache) *Handler {
	return &Handler{
		fetcher: fetcher,
		cache:   cache,
	}
}

func (h *Handler) SetConfig(config interface{}) {
	h.config = config
}

func (h *Handler) GetRegions(c *gin.Context) {
	cacheKey := "regions"
	if cached, ok := h.cache.Get(cacheKey); ok {
		c.JSON(http.StatusOK, gin.H{
			"regions":    cached,
			"from_cache": true,
		})
		return
	}

	regions, err := aws.GetRegions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.cache.Set(cacheKey, regions)
	c.JSON(http.StatusOK, gin.H{
		"regions":    regions,
		"from_cache": false,
	})
}

func (h *Handler) GetServices(c *gin.Context) {
	region := c.DefaultQuery("region", "us-east-1")
	cacheKey := "services:" + region

	if cached, ok := h.cache.Get(cacheKey); ok {
		c.JSON(http.StatusOK, gin.H{
			"services":   cached,
			"from_cache": true,
		})
		return
	}

	services, err := h.fetcher.GetServices(c.Request.Context(), region)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.cache.Set(cacheKey, services)
	c.JSON(http.StatusOK, gin.H{
		"services":   services,
		"from_cache": false,
	})
}

func (h *Handler) GetQuotas(c *gin.Context) {
	regionParam := c.Query("region")
	serviceFilter := c.Query("service")
	search := c.Query("search")

	var regions []string
	if regionParam == "" || regionParam == "all" {
		regionList, err := aws.GetRegions(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, r := range regionList {
			regions = append(regions, r.Code)
		}
	} else {
		regions = strings.Split(regionParam, ",")
	}

	cacheKey := "quotas:" + regionParam + ":" + serviceFilter
	var quotas []model.Quota
	fromCache := false

	if cached, ok := h.cache.Get(cacheKey); ok {
		if quotas, ok = cached.([]model.Quota); !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid cache data type"})
			return
		}
		fromCache = true
	} else {
		var err error
		quotas, err = h.fetcher.GetQuotasForAllRegions(c.Request.Context(), regions, serviceFilter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		h.cache.Set(cacheKey, quotas)
	}

	if search != "" {
		search = strings.ToLower(search)
		filtered := make([]model.Quota, 0)
		for _, q := range quotas {
			if strings.Contains(strings.ToLower(q.QuotaName), search) ||
				strings.Contains(strings.ToLower(q.ServiceName), search) ||
				strings.Contains(strings.ToLower(q.ServiceCode), search) {
				filtered = append(filtered, q)
			}
		}
		quotas = filtered
	}

	c.JSON(http.StatusOK, model.QuotaResponse{
		Quotas:    quotas,
		Total:     len(quotas),
		FetchedAt: time.Now(),
		FromCache: fromCache,
	})
}

func (h *Handler) Refresh(c *gin.Context) {
	h.cache.Clear()
	c.JSON(http.StatusOK, gin.H{
		"message": "Cache cleared successfully",
	})
}

func (h *Handler) GetConfig(c *gin.Context) {
	if h.config == nil {
		c.JSON(http.StatusOK, gin.H{
			"default_region":  "us-east-1",
			"default_service": "ec2",
		})
		return
	}
	c.JSON(http.StatusOK, h.config)
}
