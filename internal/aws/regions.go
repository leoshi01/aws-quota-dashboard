package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
)

func GetRegions(ctx context.Context) ([]model.Region, error) {
	cfg, err := LoadConfig(ctx, "us-east-1")
	if err != nil {
		return nil, err
	}

	client := ec2.NewFromConfig(cfg)
	output, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: boolPtr(false),
	})
	if err != nil {
		return nil, err
	}

	regions := make([]model.Region, 0, len(output.Regions))
	for _, r := range output.Regions {
		regions = append(regions, model.Region{
			Code: *r.RegionName,
			Name: *r.RegionName,
		})
	}
	return regions, nil
}

func boolPtr(b bool) *bool {
	return &b
}
