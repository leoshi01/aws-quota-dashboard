package aws

import (
	"context"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
)

// QuotaCodeToServiceMapping maps quota codes to their service and usage type
// This helps identify which direct API to call for specific quotas
var QuotaCodeToServiceMapping = map[string]UsageHandler{
	// EKS
	"L-1194D53C": {ServiceCode: "eks", Handler: getEKSClustersUsage},

	// EC2
	"L-1216C47A": {ServiceCode: "ec2", Handler: getEC2RunningInstancesUsage},
	"L-0263D0A3": {ServiceCode: "ec2", Handler: getElasticIPsUsage},

	// EBS
	"L-D18FCD1D": {ServiceCode: "ebs", Handler: getEBSGP2Usage},
	"L-7A658B76": {ServiceCode: "ebs", Handler: getEBSGP3Usage},
	"L-FD252861": {ServiceCode: "ebs", Handler: getEBSIO1Usage},
	"L-09BD8365": {ServiceCode: "ebs", Handler: getEBSIO2Usage},

	// VPC
	"L-F678F1CE": {ServiceCode: "vpc", Handler: getVPCsUsage},
	"L-DF5E4CA3": {ServiceCode: "vpc", Handler: getNetworkInterfacesUsage},
	"L-E79EC296": {ServiceCode: "vpc", Handler: getSecurityGroupsUsage},

	// ELB
	"L-53DA6B97": {ServiceCode: "elasticloadbalancing", Handler: getALBsUsage},
	"L-69A177A2": {ServiceCode: "elasticloadbalancing", Handler: getNLBsUsage},
	"L-B22855CB": {ServiceCode: "elasticloadbalancing", Handler: getTargetGroupsUsage},

	// Auto Scaling
	"L-CDE20ADC": {ServiceCode: "autoscaling", Handler: getAutoScalingGroupsUsage},
}

type UsageHandler struct {
	ServiceCode string
	Handler     func(context.Context, aws.Config, string) (float64, error)
}

// GetUsageDirectly attempts to get usage via direct API calls
func (f *QuotaFetcher) GetUsageDirectly(ctx context.Context, region string, quota *model.Quota) (float64, error) {
	handler, exists := QuotaCodeToServiceMapping[quota.QuotaCode]
	if !exists {
		return 0, nil // No direct handler available
	}

	// Only call if service codes match
	if handler.ServiceCode != quota.ServiceCode {
		return 0, nil
	}

	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return 0, err
	}

	usage, err := handler.Handler(ctx, cfg, region)
	if err != nil {
		log.Printf("Direct API failed for %s/%s: %v", quota.ServiceCode, quota.QuotaCode, err)
		return 0, err
	}

	return usage, nil
}

// ============================================================================
// EKS Usage Handlers
// ============================================================================

func getEKSClustersUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := eks.NewFromConfig(cfg)
	result, err := client.ListClusters(ctx, &eks.ListClustersInput{})
	if err != nil {
		return 0, err
	}
	return float64(len(result.Clusters)), nil
}

// ============================================================================
// EC2 Usage Handlers
// ============================================================================

func getEC2RunningInstancesUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	}

	count := 0
	paginator := ec2.NewDescribeInstancesPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		for _, reservation := range output.Reservations {
			count += len(reservation.Instances)
		}
	}

	return float64(count), nil
}

func getElasticIPsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := ec2.NewFromConfig(cfg)
	result, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		return 0, err
	}
	return float64(len(result.Addresses)), nil
}

// ============================================================================
// EBS Usage Handlers
// ============================================================================

func getEBSGP2Usage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "gp2")
}

func getEBSGP3Usage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "gp3")
}

func getEBSIO1Usage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "io1")
}

func getEBSIO2Usage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "io2")
}

func getEBSVolumeUsageByType(ctx context.Context, cfg aws.Config, volumeType string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("volume-type"),
				Values: []string{volumeType},
			},
		},
	}

	totalSizeGB := int64(0)
	paginator := ec2.NewDescribeVolumesPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		for _, volume := range output.Volumes {
			if volume.Size != nil {
				totalSizeGB += int64(*volume.Size)
			}
		}
	}

	// Convert to TiB (quota is usually in TiB)
	totalSizeTiB := float64(totalSizeGB) / 1024.0
	return totalSizeTiB, nil
}

// ============================================================================
// VPC Usage Handlers
// ============================================================================

func getVPCsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	count := 0
	paginator := ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Vpcs)
	}

	return float64(count), nil
}

func getNetworkInterfacesUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	count := 0
	paginator := ec2.NewDescribeNetworkInterfacesPaginator(client, &ec2.DescribeNetworkInterfacesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.NetworkInterfaces)
	}

	return float64(count), nil
}

func getSecurityGroupsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	count := 0
	paginator := ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.SecurityGroups)
	}

	return float64(count), nil
}

// ============================================================================
// ELB Usage Handlers
// ============================================================================

func getALBsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	return getLoadBalancersUsageByType(ctx, cfg, "application")
}

func getNLBsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	return getLoadBalancersUsageByType(ctx, cfg, "network")
}

func getLoadBalancersUsageByType(ctx context.Context, cfg aws.Config, lbType string) (float64, error) {
	client := elasticloadbalancingv2.NewFromConfig(cfg)

	count := 0
	paginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		for _, lb := range output.LoadBalancers {
			if strings.EqualFold(string(lb.Type), lbType) {
				count++
			}
		}
	}

	return float64(count), nil
}

func getTargetGroupsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := elasticloadbalancingv2.NewFromConfig(cfg)

	count := 0
	paginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.TargetGroups)
	}

	return float64(count), nil
}

// ============================================================================
// Auto Scaling Usage Handlers
// ============================================================================

func getAutoScalingGroupsUsage(ctx context.Context, cfg aws.Config, region string) (float64, error) {
	client := autoscaling.NewFromConfig(cfg)

	count := 0
	paginator := autoscaling.NewDescribeAutoScalingGroupsPaginator(client, &autoscaling.DescribeAutoScalingGroupsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.AutoScalingGroups)
	}

	return float64(count), nil
}
