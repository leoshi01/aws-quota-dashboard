package aws

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
	"golang.org/x/sync/errgroup"
)

type QuotaFetcher struct {
	maxConcurrency int
}

func NewQuotaFetcher(maxConcurrency int) *QuotaFetcher {
	if maxConcurrency <= 0 {
		maxConcurrency = 10
	}
	return &QuotaFetcher{maxConcurrency: maxConcurrency}
}

func (f *QuotaFetcher) GetServices(ctx context.Context, region string) ([]model.Service, error) {
	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return nil, err
	}

	client := servicequotas.NewFromConfig(cfg)
	var services []model.Service
	paginator := servicequotas.NewListServicesPaginator(client, &servicequotas.ListServicesInput{})

	for paginator.HasMorePages() {
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
	var quotas []model.Quota
	paginator := servicequotas.NewListServiceQuotasPaginator(client, &servicequotas.ListServiceQuotasInput{
		ServiceCode: &svc.Code,
	})

	// Create CloudWatch client for this region
	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return quotas, err
	}
	cwClient := cloudwatch.NewFromConfig(cfg)

	log.Printf("Fetching quotas for service: %s (%s) in region: %s", svc.Name, svc.Code, region)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return quotas, err
		}
		for _, q := range output.Quotas {
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

			// Try to get usage metrics from CloudWatch first
			if q.UsageMetric != nil {
				f.enrichWithUsageFromCloudWatch(ctx, cwClient, q.UsageMetric, &quota)
			}

			// Fallback to direct API if CloudWatch didn't provide usage data
			if quota.Usage == 0 && !quota.HasUsageMetrics {
				f.enrichWithDirectAPI(ctx, region, &quota)
			}

			quotas = append(quotas, quota)
		}
	}
	return quotas, nil
}

func (f *QuotaFetcher) enrichWithUsageFromCloudWatch(ctx context.Context, cwClient *cloudwatch.Client, usageMetric *sqtypes.MetricInfo, quota *model.Quota) {
	if usageMetric.MetricNamespace == nil || usageMetric.MetricName == nil {
		return
	}

	quota.HasUsageMetrics = true

	stat := getStatisticFromRecommendation(usageMetric.MetricStatisticRecommendation)
	dimensions := buildCloudWatchDimensions(usageMetric.MetricDimensions)

	result, err := f.queryCloudWatch(ctx, cwClient, usageMetric, dimensions, stat)
	if err != nil {
		log.Printf("CloudWatch query failed for %s/%s: %v",
			safeString(usageMetric.MetricNamespace),
			safeString(usageMetric.MetricName), err)
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

	// 只有当直接API支持该配额时才设置数据
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
	if value > 0 {
		updateQuotaUsage(quota, value)
		log.Printf("  ✓ Usage found: %.2f / %.2f (%.1f%%)",
			quota.Usage, quota.Value, quota.UsagePercentage)
	}
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

func (f *QuotaFetcher) GetQuotasForAllRegions(ctx context.Context, regions []string, serviceFilter string) ([]model.Quota, error) {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(f.maxConcurrency)

	quotasChan := make(chan []model.Quota, len(regions))

	for _, region := range regions {
		region := region
		g.Go(func() error {
			quotas, err := f.GetQuotasForRegion(ctx, region, serviceFilter)
			if err != nil {
				return nil // Don't fail entire operation for one region
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

	return allQuotas, nil
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
