package aws

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
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
			
			// Try to get usage metrics
			f.enrichWithUsageMetrics(ctx, client, &quota)
			
			quotas = append(quotas, quota)
		}
	}
	return quotas, nil
}

func (f *QuotaFetcher) enrichWithUsageMetrics(ctx context.Context, client *servicequotas.Client, quota *model.Quota) {
	// Skip if quota code is empty
	if quota.QuotaCode == "" {
		return
	}

	input := &servicequotas.GetAWSDefaultServiceQuotaInput{
		ServiceCode: &quota.ServiceCode,
		QuotaCode:   &quota.QuotaCode,
	}

	output, err := client.GetAWSDefaultServiceQuota(ctx, input)
	if err != nil {
		// Usage metrics may not be available for all quotas
		return
	}

	if output.Quota != nil && output.Quota.UsageMetric != nil {
		// Usage metrics are available
		quota.HasUsageMetrics = true
		
		// Try to get the actual usage value
		if output.Quota.UsageMetric.MetricStatisticRecommendation != nil {
			// The usage metric data would typically come from CloudWatch
			// For now, we mark that metrics are available
			// In a production system, you'd query CloudWatch here
			
			// Note: The actual usage would require CloudWatch API calls
			// which can be added based on the metric dimensions
		}
	}

	// Alternative: try to get applied quota which might have usage info
	appliedInput := &servicequotas.GetServiceQuotaInput{
		ServiceCode: &quota.ServiceCode,
		QuotaCode:   &quota.QuotaCode,
	}

	appliedOutput, err := client.GetServiceQuota(ctx, appliedInput)
	if err == nil && appliedOutput.Quota != nil && appliedOutput.Quota.UsageMetric != nil {
		quota.HasUsageMetrics = true
	}

	// Calculate usage percentage if both usage and value are available
	if quota.Usage > 0 && quota.Value > 0 {
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
