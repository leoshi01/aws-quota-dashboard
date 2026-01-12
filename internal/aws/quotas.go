package aws

import (
	"context"
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

			// Try to get usage metrics from CloudWatch
			if q.UsageMetric != nil {
				f.enrichWithUsageFromCloudWatch(ctx, cwClient, q.UsageMetric, &quota)
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

	// Determine the statistic to use (default to Maximum if not specified)
	stat := "Maximum"
	if usageMetric.MetricStatisticRecommendation != nil && *usageMetric.MetricStatisticRecommendation != "" {
		stat = *usageMetric.MetricStatisticRecommendation
	}

	// Prepare dimensions
	var dimensions []cwtypes.Dimension
	if usageMetric.MetricDimensions != nil {
		for key, value := range usageMetric.MetricDimensions {
			k := key
			v := value
			dimensions = append(dimensions, cwtypes.Dimension{
				Name:  &k,
				Value: &v,
			})
		}
	}

	// Query CloudWatch for the last data point (last 7 days)
	endTime := time.Now()
	startTime := endTime.Add(-7 * 24 * time.Hour)

	input := &cloudwatch.GetMetricStatisticsInput{
		Namespace:  usageMetric.MetricNamespace,
		MetricName: usageMetric.MetricName,
		Dimensions: dimensions,
		StartTime:  &startTime,
		EndTime:    &endTime,
		Period:     aws.Int32(3600), // 1 hour period
		Statistics: []cwtypes.Statistic{cwtypes.Statistic(stat)},
	}

	// Query CloudWatch (with timeout protection)
	result, err := cwClient.GetMetricStatistics(ctx, input)
	if err != nil {
		// CloudWatch query failed, but we still mark that metrics exist
		return
	}

	// Find the most recent data point
	if len(result.Datapoints) > 0 {
		var latestDatapoint *cwtypes.Datapoint
		for i := range result.Datapoints {
			if latestDatapoint == nil || result.Datapoints[i].Timestamp.After(*latestDatapoint.Timestamp) {
				latestDatapoint = &result.Datapoints[i]
			}
		}

		if latestDatapoint != nil {
			// Extract the value based on the statistic
			switch stat {
			case "Maximum":
				if latestDatapoint.Maximum != nil {
					quota.Usage = *latestDatapoint.Maximum
				}
			case "Average":
				if latestDatapoint.Average != nil {
					quota.Usage = *latestDatapoint.Average
				}
			case "Sum":
				if latestDatapoint.Sum != nil {
					quota.Usage = *latestDatapoint.Sum
				}
			case "Minimum":
				if latestDatapoint.Minimum != nil {
					quota.Usage = *latestDatapoint.Minimum
				}
			default:
				if latestDatapoint.Maximum != nil {
					quota.Usage = *latestDatapoint.Maximum
				}
			}

			// Calculate usage percentage
			if quota.Value > 0 {
				quota.UsagePercentage = (quota.Usage / quota.Value) * 100
			}
		}
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
