package aws

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

type QuotaFetcher struct {
	maxConcurrency int
	limiter        *rate.Limiter
}

func NewQuotaFetcher(maxConcurrency int) *QuotaFetcher {
	if maxConcurrency <= 0 {
		maxConcurrency = 10
	}
	return &QuotaFetcher{
		maxConcurrency: maxConcurrency,
		limiter:        rate.NewLimiter(rate.Limit(5), 10),
	}
}

func (f *QuotaFetcher) GetServices(ctx context.Context, region string) ([]model.Service, error) {
	if err := f.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return nil, err
	}

	client := servicequotas.NewFromConfig(cfg)
	var services []model.Service
	paginator := servicequotas.NewListServicesPaginator(client, &servicequotas.ListServicesInput{})

	for paginator.HasMorePages() {
		if err := f.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range output.Services {
			services = append(services, model.Service{
				Code: *s.ServiceCode,
				Name: *s.ServiceName,
			})
		}
	}
	return services, nil
}

func (f *QuotaFetcher) GetQuotasForRegion(ctx context.Context, region string, serviceFilter string) ([]model.Quota, error) {
	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return nil, err
	}

	client := servicequotas.NewFromConfig(cfg)

	services, err := f.GetServices(ctx, region)
	if err != nil {
		return nil, err
	}

	if serviceFilter != "" {
		filtered := make([]model.Service, 0)
		for _, s := range services {
			if strings.EqualFold(s.Code, serviceFilter) {
				filtered = append(filtered, s)
			}
		}
		services = filtered
	}

	var quotas []model.Quota
	for _, svc := range services {
		svcQuotas, err := f.getQuotasForService(ctx, client, region, svc)
		if err != nil {
			continue // Skip services that fail
		}
		quotas = append(quotas, svcQuotas...)
	}

	return quotas, nil
}

func (f *QuotaFetcher) getQuotasForService(ctx context.Context, client *servicequotas.Client, region string, svc model.Service) ([]model.Quota, error) {
	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return nil, err
	}
	cwClient := cloudwatch.NewFromConfig(cfg)

	log.Printf("Fetching quotas for service: %s (%s) in region: %s", svc.Name, svc.Code, region)

	quotaMap := make(map[string]sqtypes.ServiceQuota)

	f.fetchDefaultQuotas(ctx, client, svc.Code, quotaMap)
	f.fetchAppliedQuotas(ctx, client, svc.Code, quotaMap)

	return f.buildQuotaList(ctx, cwClient, region, svc, quotaMap), nil
}

func (f *QuotaFetcher) fetchDefaultQuotas(ctx context.Context, client *servicequotas.Client, serviceCode string, quotaMap map[string]sqtypes.ServiceQuota) {
	paginator := servicequotas.NewListAWSDefaultServiceQuotasPaginator(client, &servicequotas.ListAWSDefaultServiceQuotasInput{
		ServiceCode: &serviceCode,
	})
	for paginator.HasMorePages() {
		if err := f.limiter.Wait(ctx); err != nil {
			return
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("Failed to get default quotas for %s: %v", serviceCode, err)
			return
		}
		for i := range output.Quotas {
			q := output.Quotas[i]
			if q.QuotaCode != nil {
				quotaMap[*q.QuotaCode] = q
			}
		}
	}
}

func (f *QuotaFetcher) fetchAppliedQuotas(ctx context.Context, client *servicequotas.Client, serviceCode string, quotaMap map[string]sqtypes.ServiceQuota) {
	paginator := servicequotas.NewListServiceQuotasPaginator(client, &servicequotas.ListServiceQuotasInput{
		ServiceCode: &serviceCode,
	})
	for paginator.HasMorePages() {
		if err := f.limiter.Wait(ctx); err != nil {
			return
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("Failed to get applied quotas for %s: %v", serviceCode, err)
			return
		}
		for i := range output.Quotas {
			q := output.Quotas[i]
			if q.QuotaCode != nil {
				quotaMap[*q.QuotaCode] = q
			}
		}
	}
}

func (f *QuotaFetcher) buildQuotaList(ctx context.Context, cwClient *cloudwatch.Client, region string, svc model.Service, quotaMap map[string]sqtypes.ServiceQuota) []model.Quota {
	var quotas []model.Quota
	for _, q := range quotaMap {
		quota := model.Quota{
			Region:      region,
			ServiceCode: svc.Code,
			ServiceName: svc.Name,
			QuotaName:   safeString(q.QuotaName),
			QuotaCode:   safeString(q.QuotaCode),
			Unit:        safeString(q.Unit),
			Adjustable:  q.Adjustable,
			Global:      q.GlobalQuota,
		}
		if q.Value != nil {
			quota.Value = *q.Value
		}

		f.enrichWithDirectAPI(ctx, region, &quota)

		if !quota.HasUsageMetrics && q.UsageMetric != nil {
			f.enrichWithUsageFromCloudWatch(ctx, cwClient, q.UsageMetric, &quota)
		}

		quotas = append(quotas, quota)
	}
	return quotas
}

func (f *QuotaFetcher) enrichWithUsageFromCloudWatch(ctx context.Context, cwClient *cloudwatch.Client, usageMetric *sqtypes.MetricInfo, quota *model.Quota) {
	if usageMetric.MetricNamespace == nil || usageMetric.MetricName == nil {
		return
	}

	stat := getStatisticFromRecommendation(usageMetric.MetricStatisticRecommendation)
	dimensions := buildCloudWatchDimensions(usageMetric.MetricDimensions)

	result, err := f.queryCloudWatch(ctx, cwClient, usageMetric, dimensions, stat)
	if err != nil {
		log.Printf("CloudWatch query failed for %s/%s: %v",
			safeString(usageMetric.MetricNamespace),
			safeString(usageMetric.MetricName), err)
		return
	}

	if len(result.Datapoints) == 0 {
		log.Printf("CloudWatch no datapoints for %s - %s", quota.ServiceCode, quota.QuotaName)
		return
	}

	log.Printf("CloudWatch query for %s - %s: namespace=%s, metric=%s, datapoints=%d",
		quota.ServiceCode, quota.QuotaName,
		safeString(usageMetric.MetricNamespace),
		safeString(usageMetric.MetricName),
		len(result.Datapoints))

	f.processCloudWatchResult(result, stat, quota)
}

func (f *QuotaFetcher) enrichWithDirectAPI(ctx context.Context, region string, quota *model.Quota) {
	usage, supported, err := f.GetUsageDirectly(ctx, region, quota)
	if err != nil {
		log.Printf("Direct API query failed for %s/%s: %v", quota.ServiceCode, quota.QuotaCode, err)
		return
	}

	// Only set data when direct API supports this quota
	if supported {
		quota.HasUsageMetrics = true
		quota.Usage = usage
		if quota.Value > 0 {
			quota.UsagePercentage = (quota.Usage / quota.Value) * 100
		}
		log.Printf("  ✓ Usage from Direct API: %.2f / %.2f (%.1f%%) - %s",
			quota.Usage, quota.Value, quota.UsagePercentage, quota.QuotaName)
	}
}

func getStatisticFromRecommendation(recommendation *string) string {
	if recommendation != nil && *recommendation != "" {
		return *recommendation
	}
	return "Maximum"
}

func buildCloudWatchDimensions(metricDimensions map[string]string) []cwtypes.Dimension {
	var dimensions []cwtypes.Dimension
	for key, value := range metricDimensions {
		k := key
		v := value
		dimensions = append(dimensions, cwtypes.Dimension{
			Name:  &k,
			Value: &v,
		})
	}
	return dimensions
}

func (f *QuotaFetcher) queryCloudWatch(ctx context.Context, cwClient *cloudwatch.Client, usageMetric *sqtypes.MetricInfo, dimensions []cwtypes.Dimension, stat string) (*cloudwatch.GetMetricStatisticsOutput, error) {
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	input := &cloudwatch.GetMetricStatisticsInput{
		Namespace:  usageMetric.MetricNamespace,
		MetricName: usageMetric.MetricName,
		Dimensions: dimensions,
		StartTime:  &startTime,
		EndTime:    &endTime,
		Period:     aws.Int32(300),
		Statistics: []cwtypes.Statistic{cwtypes.Statistic(stat)},
	}

	return cwClient.GetMetricStatistics(ctx, input)
}

func (f *QuotaFetcher) processCloudWatchResult(result *cloudwatch.GetMetricStatisticsOutput, stat string, quota *model.Quota) {
	if len(result.Datapoints) == 0 {
		log.Printf("  ✗ No datapoints found for %s - %s", quota.ServiceCode, quota.QuotaName)
		return
	}

	latestDatapoint := findLatestDatapoint(result.Datapoints)
	if latestDatapoint == nil {
		return
	}

	value := extractValueFromDatapoint(latestDatapoint, stat)
	quota.HasUsageMetrics = true
	updateQuotaUsage(quota, value)
	log.Printf("  ✓ Usage found: %.2f / %.2f (%.1f%%)",
		quota.Usage, quota.Value, quota.UsagePercentage)
}

func findLatestDatapoint(datapoints []cwtypes.Datapoint) *cwtypes.Datapoint {
	var latest *cwtypes.Datapoint
	for i := range datapoints {
		if latest == nil || datapoints[i].Timestamp.After(*latest.Timestamp) {
			latest = &datapoints[i]
		}
	}
	return latest
}

func extractValueFromDatapoint(datapoint *cwtypes.Datapoint, stat string) float64 {
	switch stat {
	case "Maximum":
		if datapoint.Maximum != nil {
			return *datapoint.Maximum
		}
	case "Average":
		if datapoint.Average != nil {
			return *datapoint.Average
		}
	case "Sum":
		if datapoint.Sum != nil {
			return *datapoint.Sum
		}
	case "Minimum":
		if datapoint.Minimum != nil {
			return *datapoint.Minimum
		}
	default:
		if datapoint.Maximum != nil {
			return *datapoint.Maximum
		}
	}
	return 0
}

func updateQuotaUsage(quota *model.Quota, value float64) {
	quota.Usage = value
	if quota.Value > 0 {
		quota.UsagePercentage = (quota.Usage / quota.Value) * 100
	}
}

type FetchResult struct {
	Quotas   []model.Quota
	Warnings []string
}

func (f *QuotaFetcher) GetQuotasForAllRegions(ctx context.Context, regions []string, serviceFilter string) (*FetchResult, error) {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(f.maxConcurrency)

	quotasChan := make(chan []model.Quota, len(regions))
	var warnings []string
	var warningsMu sync.Mutex

	for _, region := range regions {
		region := region
		g.Go(func() error {
			quotas, err := f.GetQuotasForRegion(ctx, region, serviceFilter)
			if err != nil {
				warningsMu.Lock()
				warnings = append(warnings, fmt.Sprintf("Failed to fetch quotas for region %s: %v", region, err))
				warningsMu.Unlock()
				return nil
			}
			quotasChan <- quotas
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	close(quotasChan)

	var allQuotas []model.Quota
	for quotas := range quotasChan {
		allQuotas = append(allQuotas, quotas...)
	}

	allQuotas = deduplicateGlobalQuotas(allQuotas)

	return &FetchResult{
		Quotas:   allQuotas,
		Warnings: warnings,
	}, nil
}

func deduplicateGlobalQuotas(quotas []model.Quota) []model.Quota {
	seen := make(map[string]bool)
	var result []model.Quota

	for _, q := range quotas {
		if q.Global {
			key := q.ServiceCode + ":" + q.QuotaCode
			if seen[key] {
				continue
			}
			seen[key] = true
			q.Region = "global"
		}
		result = append(result, q)
	}
	return result
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
