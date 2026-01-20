package aws

import (
	"context"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
)

// QuotaCodeToServiceMapping maps quota codes to their service and usage type
// This helps identify which direct API to call for specific quotas
var QuotaCodeToServiceMapping = map[string]UsageHandler{
	// EKS
	"L-1194D53C": {ServiceCode: "eks", Handler: getEKSClustersUsage},
	"L-6D3F50E6": {ServiceCode: "eks", Handler: getEKSNodeGroupsUsage},
	"L-23414FF3": {ServiceCode: "eks", Handler: getEKSFargateProfilesUsage},
	"L-6E77F4DE": {ServiceCode: "eks", Handler: getEKSAddonsUsage},

	// EC2
	"L-1216C47A": {ServiceCode: "ec2", Handler: getEC2RunningInstancesUsage},
	"L-0263D0A3": {ServiceCode: "ec2", Handler: getElasticIPsUsage},
	"L-0E3CBAB9": {ServiceCode: "ec2", Handler: getEC2KeyPairsUsage},
	"L-0DA580E9": {ServiceCode: "ec2", Handler: getEC2AMIsUsage},
	"L-309BACF6": {ServiceCode: "ec2", Handler: getEC2SnapshotsUsage},
	"L-407747CB": {ServiceCode: "ec2", Handler: getEC2InternetGatewaysUsage},
	"L-FE5A380F": {ServiceCode: "ec2", Handler: getEC2NATGatewaysUsage},

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

	// S3
	"L-DC2B2D3D": {ServiceCode: "s3", Handler: getS3BucketsUsage},

	// Lambda
	"L-9FEE3D26": {ServiceCode: "lambda", Handler: getLambdaFunctionsUsage},

	// RDS
	"L-7B6409FD": {ServiceCode: "rds", Handler: getRDSInstancesUsage},
	"L-952B80B8": {ServiceCode: "rds", Handler: getRDSClustersUsage},

	// DynamoDB
	"L-F98FE922": {ServiceCode: "dynamodb", Handler: getDynamoDBTablesUsage},

	// CloudFront
	"L-5B2E3F44": {ServiceCode: "cloudfront", Handler: getCloudFrontDistributionsUsage},

	// Route53
	"L-ACB674F3": {ServiceCode: "route53", Handler: getRoute53HostedZonesUsage},

	// IAM
	"L-4019AD8D": {ServiceCode: "iam", Handler: getIAMUsersUsage},
	"L-FE177D64": {ServiceCode: "iam", Handler: getIAMRolesUsage},
	"L-0DA4ABF3": {ServiceCode: "iam", Handler: getIAMGroupsUsage},
	"L-D0B7243C": {ServiceCode: "iam", Handler: getIAMPoliciesUsage},

	// SNS
	"L-61103206": {ServiceCode: "sns", Handler: getSNSTopicsUsage},

	// SQS
	"L-75826ACE": {ServiceCode: "sqs", Handler: getSQSQueuesUsage},

	// ECR
	"L-CFEB8E8D": {ServiceCode: "ecr", Handler: getECRRepositoriesUsage},
}

type UsageHandler struct {
	ServiceCode string
	Handler     func(context.Context, aws.Config, string) (float64, error)
}

// GetUsageDirectly attempts to get usage via direct API calls
// Returns (usage, true, nil) if successful, (0, false, nil) if not supported
func (f *QuotaFetcher) GetUsageDirectly(ctx context.Context, region string, quota *model.Quota) (float64, bool, error) {
	handler, exists := QuotaCodeToServiceMapping[quota.QuotaCode]
	if !exists {
		return 0, false, nil // No direct handler available
	}

	// Only call if service codes match
	if handler.ServiceCode != quota.ServiceCode {
		return 0, false, nil
	}

	cfg, err := LoadConfig(ctx, region)
	if err != nil {
		return 0, false, err
	}

	usage, err := handler.Handler(ctx, cfg, region)
	if err != nil {
		log.Printf("Direct API failed for %s/%s: %v", quota.ServiceCode, quota.QuotaCode, err)
		return 0, false, err
	}

	return usage, true, nil // Return true indicating successful data retrieval (even if usage is 0)
}

// ============================================================================
// EKS Usage Handlers
// ============================================================================

func getEKSClustersUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := eks.NewFromConfig(cfg)
	result, err := client.ListClusters(ctx, &eks.ListClustersInput{})
	if err != nil {
		return 0, err
	}
	return float64(len(result.Clusters)), nil
}

func getEKSNodeGroupsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := eks.NewFromConfig(cfg)
	return getEKSClusterResourceCount(ctx, client, func(clusterName string) (int, error) {
		ngPaginator := eks.NewListNodegroupsPaginator(client, &eks.ListNodegroupsInput{
			ClusterName: aws.String(clusterName),
		})
		count := 0
		for ngPaginator.HasMorePages() {
			ngPage, err := ngPaginator.NextPage(ctx)
			if err != nil {
				return 0, err
			}
			count += len(ngPage.Nodegroups)
		}
		return count, nil
	})
}

func getEKSFargateProfilesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := eks.NewFromConfig(cfg)
	return getEKSClusterResourceCount(ctx, client, func(clusterName string) (int, error) {
		fpPaginator := eks.NewListFargateProfilesPaginator(client, &eks.ListFargateProfilesInput{
			ClusterName: aws.String(clusterName),
		})
		count := 0
		for fpPaginator.HasMorePages() {
			fpPage, err := fpPaginator.NextPage(ctx)
			if err != nil {
				return 0, err
			}
			count += len(fpPage.FargateProfileNames)
		}
		return count, nil
	})
}

func getEKSAddonsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := eks.NewFromConfig(cfg)
	return getEKSClusterResourceCount(ctx, client, func(clusterName string) (int, error) {
		addonPaginator := eks.NewListAddonsPaginator(client, &eks.ListAddonsInput{
			ClusterName: aws.String(clusterName),
		})
		count := 0
		for addonPaginator.HasMorePages() {
			addonPage, err := addonPaginator.NextPage(ctx)
			if err != nil {
				return 0, err
			}
			count += len(addonPage.Addons)
		}
		return count, nil
	})
}

// Helper function to count resources across all EKS clusters
func getEKSClusterResourceCount(ctx context.Context, client *eks.Client, countFunc func(string) (int, error)) (float64, error) {
	clusterPaginator := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})

	total := 0
	for clusterPaginator.HasMorePages() {
		clusterPage, err := clusterPaginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}

		for _, clusterName := range clusterPage.Clusters {
			count, err := countFunc(clusterName)
			if err != nil {
				log.Printf("Failed to count resources for cluster %s: %v", clusterName, err)
				continue
			}
			total += count
		}
	}

	return float64(total), nil
}

// ============================================================================
// EC2 Usage Handlers
// ============================================================================

func getEC2RunningInstancesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	return getEC2VCPUUsageByInstanceFamily(ctx, cfg, standardInstanceFamilies)
}

// standardInstanceFamilies contains instance type prefixes for Standard On-Demand vCPU quota (L-1216C47A)
var standardInstanceFamilies = []string{"a", "c", "d", "h", "i", "m", "r", "t", "z"}

// getEC2VCPUUsageByInstanceFamily calculates total vCPU usage for specified instance families
func getEC2VCPUUsageByInstanceFamily(ctx context.Context, cfg aws.Config, families []string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	instanceTypeCounts, cpuOptionsByType, err := getRunningInstanceTypeCounts(ctx, client, families)
	if err != nil {
		return 0, err
	}

	if len(instanceTypeCounts) == 0 {
		return 0, nil
	}

	instanceTypes := collectInstanceTypes(instanceTypeCounts)

	vcpuMap, err := getInstanceTypeVCPUs(ctx, client, instanceTypes)
	if err != nil {
		log.Printf("Failed to describe instance types for vCPU lookup: %v", err)
	}

	totalVCPUs := calculateTotalVCPUs(instanceTypeCounts, vcpuMap, cpuOptionsByType)
	return float64(totalVCPUs), nil
}

func getRunningInstanceTypeCounts(ctx context.Context, client *ec2.Client, families []string) (map[string]int, map[string]ec2types.CpuOptions, error) {
	input := &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	}

	instanceTypeCounts := make(map[string]int)
	cpuOptionsByType := make(map[string]ec2types.CpuOptions)
	paginator := ec2.NewDescribeInstancesPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				if instance.InstanceType == "" {
					continue
				}
				instanceType := string(instance.InstanceType)
				if !isInstanceInFamilies(instanceType, families) {
					continue
				}
				instanceTypeCounts[instanceType]++
				if instance.CpuOptions != nil {
					cpuOptionsByType[instanceType] = *instance.CpuOptions
				}
			}
		}
	}

	return instanceTypeCounts, cpuOptionsByType, nil
}

func collectInstanceTypes(instanceTypeCounts map[string]int) []string {
	instanceTypes := make([]string, 0, len(instanceTypeCounts))
	for instanceType := range instanceTypeCounts {
		instanceTypes = append(instanceTypes, instanceType)
	}
	return instanceTypes
}

func calculateTotalVCPUs(instanceTypeCounts map[string]int, vcpuMap map[string]int32, cpuOptionsByType map[string]ec2types.CpuOptions) int64 {
	totalVCPUs := int64(0)
	for instanceType, count := range instanceTypeCounts {
		if vcpus, ok := vcpuMap[instanceType]; ok && vcpus > 0 {
			totalVCPUs += int64(vcpus) * int64(count)
			continue
		}
		if options, ok := cpuOptionsByType[instanceType]; ok && options.CoreCount != nil && options.ThreadsPerCore != nil {
			vcpus := int64(*options.CoreCount) * int64(*options.ThreadsPerCore)
			totalVCPUs += vcpus * int64(count)
			continue
		}
		log.Printf("Missing vCPU info for instance type %s; skipping %d instances", instanceType, count)
	}
	return totalVCPUs
}

func getInstanceTypeVCPUs(ctx context.Context, client *ec2.Client, instanceTypes []string) (map[string]int32, error) {
	vcpuMap := make(map[string]int32)
	if len(instanceTypes) == 0 {
		return vcpuMap, nil
	}

	const batchSize = 100
	for start := 0; start < len(instanceTypes); start += batchSize {
		end := start + batchSize
		if end > len(instanceTypes) {
			end = len(instanceTypes)
		}
		batch := make([]ec2types.InstanceType, 0, end-start)
		for _, instanceType := range instanceTypes[start:end] {
			batch = append(batch, ec2types.InstanceType(instanceType))
		}
		output, err := client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
			InstanceTypes: batch,
		})
		if err != nil {
			return vcpuMap, err
		}
		for _, info := range output.InstanceTypes {
			if info.InstanceType == "" || info.VCpuInfo == nil || info.VCpuInfo.DefaultVCpus == nil {
				continue
			}
			vcpuMap[string(info.InstanceType)] = *info.VCpuInfo.DefaultVCpus
		}
	}

	return vcpuMap, nil
}

// isInstanceInFamilies checks if an instance type belongs to any of the specified families
func isInstanceInFamilies(instanceType string, families []string) bool {
	if len(instanceType) == 0 {
		return false
	}
	// Instance type format: <family><generation>.<size> e.g., m5.large, c6i.xlarge
	firstChar := strings.ToLower(string(instanceType[0]))
	for _, family := range families {
		if firstChar == family {
			return true
		}
	}
	return false
}

func getElasticIPsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ec2.NewFromConfig(cfg)
	result, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		return 0, err
	}
	return float64(len(result.Addresses)), nil
}

func getEC2KeyPairsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ec2.NewFromConfig(cfg)
	result, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		return 0, err
	}
	return float64(len(result.KeyPairs)), nil
}

func getEC2AMIsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	// Only count AMIs owned by this account
	owners := []string{"self"}
	count := 0

	paginator := ec2.NewDescribeImagesPaginator(client, &ec2.DescribeImagesInput{
		Owners: owners,
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Images)
	}

	return float64(count), nil
}

func getEC2SnapshotsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	// Only count snapshots owned by this account
	ownerIDs := []string{"self"}
	count := 0

	paginator := ec2.NewDescribeSnapshotsPaginator(client, &ec2.DescribeSnapshotsInput{
		OwnerIds: ownerIDs,
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Snapshots)
	}

	return float64(count), nil
}

func getEC2InternetGatewaysUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	count := 0
	paginator := ec2.NewDescribeInternetGatewaysPaginator(client, &ec2.DescribeInternetGatewaysInput{})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.InternetGateways)
	}

	return float64(count), nil
}

func getEC2NATGatewaysUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ec2.NewFromConfig(cfg)

	count := 0
	paginator := ec2.NewDescribeNatGatewaysPaginator(client, &ec2.DescribeNatGatewaysInput{})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		// Only count available NAT gateways (not deleted/failed ones)
		for _, natGw := range output.NatGateways {
			if natGw.State == ec2types.NatGatewayStateAvailable ||
				natGw.State == ec2types.NatGatewayStatePending {
				count++
			}
		}
	}

	return float64(count), nil
}

// ============================================================================
// EBS Usage Handlers
// ============================================================================

func getEBSGP2Usage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "gp2")
}

func getEBSGP3Usage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "gp3")
}

func getEBSIO1Usage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	return getEBSVolumeUsageByType(ctx, cfg, "io1")
}

func getEBSIO2Usage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

func getVPCsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

func getNetworkInterfacesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

func getSecurityGroupsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

func getALBsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	return getLoadBalancersUsageByType(ctx, cfg, "application")
}

func getNLBsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

func getTargetGroupsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

func getAutoScalingGroupsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
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

// ============================================================================
// S3 Usage Handlers
// ============================================================================

func getS3BucketsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := s3.NewFromConfig(cfg)
	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return 0, err
	}
	return float64(len(result.Buckets)), nil
}

// ============================================================================
// Lambda Usage Handlers
// ============================================================================

func getLambdaFunctionsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := lambda.NewFromConfig(cfg)

	count := 0
	paginator := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Functions)
	}

	return float64(count), nil
}

// ============================================================================
// RDS Usage Handlers
// ============================================================================

func getRDSInstancesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := rds.NewFromConfig(cfg)

	count := 0
	paginator := rds.NewDescribeDBInstancesPaginator(client, &rds.DescribeDBInstancesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.DBInstances)
	}

	return float64(count), nil
}

func getRDSClustersUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := rds.NewFromConfig(cfg)

	count := 0
	paginator := rds.NewDescribeDBClustersPaginator(client, &rds.DescribeDBClustersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.DBClusters)
	}

	return float64(count), nil
}

// ============================================================================
// DynamoDB Usage Handlers
// ============================================================================

func getDynamoDBTablesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := dynamodb.NewFromConfig(cfg)

	count := 0
	paginator := dynamodb.NewListTablesPaginator(client, &dynamodb.ListTablesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.TableNames)
	}

	return float64(count), nil
}

// ============================================================================
// CloudFront Usage Handlers
// ============================================================================

func getCloudFrontDistributionsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := cloudfront.NewFromConfig(cfg)

	count := 0
	paginator := cloudfront.NewListDistributionsPaginator(client, &cloudfront.ListDistributionsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		if output.DistributionList != nil && output.DistributionList.Items != nil {
			count += len(output.DistributionList.Items)
		}
	}

	return float64(count), nil
}

// ============================================================================
// Route53 Usage Handlers
// ============================================================================

func getRoute53HostedZonesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := route53.NewFromConfig(cfg)

	count := 0
	paginator := route53.NewListHostedZonesPaginator(client, &route53.ListHostedZonesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		// Filter out private hosted zones (optional)
		for _, zone := range output.HostedZones {
			if zone.Config == nil || !zone.Config.PrivateZone {
				count++
			}
		}
	}

	return float64(count), nil
}

// ============================================================================
// IAM Usage Handlers
// ============================================================================

func getIAMUsersUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := iam.NewFromConfig(cfg)

	count := 0
	paginator := iam.NewListUsersPaginator(client, &iam.ListUsersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Users)
	}

	return float64(count), nil
}

func getIAMRolesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := iam.NewFromConfig(cfg)

	count := 0
	paginator := iam.NewListRolesPaginator(client, &iam.ListRolesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Roles)
	}

	return float64(count), nil
}

func getIAMGroupsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := iam.NewFromConfig(cfg)

	count := 0
	paginator := iam.NewListGroupsPaginator(client, &iam.ListGroupsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Groups)
	}

	return float64(count), nil
}

func getIAMPoliciesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := iam.NewFromConfig(cfg)

	count := 0
	// Only count customer managed policies
	paginator := iam.NewListPoliciesPaginator(client, &iam.ListPoliciesInput{
		Scope: iamtypes.PolicyScopeTypeLocal,
	})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Policies)
	}

	return float64(count), nil
}

// ============================================================================
// SNS Usage Handlers
// ============================================================================

func getSNSTopicsUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := sns.NewFromConfig(cfg)

	count := 0
	paginator := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Topics)
	}

	return float64(count), nil
}

// ============================================================================
// SQS Usage Handlers
// ============================================================================

func getSQSQueuesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := sqs.NewFromConfig(cfg)

	count := 0
	paginator := sqs.NewListQueuesPaginator(client, &sqs.ListQueuesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.QueueUrls)
	}

	return float64(count), nil
}

// ============================================================================
// ECR Usage Handlers
// ============================================================================

func getECRRepositoriesUsage(ctx context.Context, cfg aws.Config, _ string) (float64, error) {
	client := ecr.NewFromConfig(cfg)

	count := 0
	paginator := ecr.NewDescribeRepositoriesPaginator(client, &ecr.DescribeRepositoriesInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		count += len(output.Repositories)
	}

	return float64(count), nil
}
