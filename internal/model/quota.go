package model

import "time"

type Quota struct {
	Region          string  `json:"region"`
	ServiceCode     string  `json:"service_code"`
	ServiceName     string  `json:"service_name"`
	QuotaName       string  `json:"quota_name"`
	QuotaCode       string  `json:"quota_code"`
	Value           float64 `json:"value"`
	Usage           float64 `json:"usage"`
	UsagePercentage float64 `json:"usage_percentage"`
	HasUsageMetrics bool    `json:"has_usage_metrics"`
	Unit            string  `json:"unit"`
	Adjustable      bool    `json:"adjustable"`
	Global          bool    `json:"global"`
}

type QuotaResponse struct {
	Quotas    []Quota   `json:"quotas"`
	Total     int       `json:"total"`
	FetchedAt time.Time `json:"fetched_at"`
	FromCache bool      `json:"from_cache"`
}

type Region struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Service struct {
	Code string `json:"code"`
	Name string `json:"name"`
}
