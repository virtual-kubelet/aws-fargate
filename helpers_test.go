//go:build e2e

// This end-to-end test creates AWS resources in the account of the credentials it is run with.
// Run it with: go test -tags e2e .

package aws_test

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// createVpcWithInternetAccess create a VPC with one subnet and internet access
// and tags all created resources
func createVpcWithInternetAccess(ctx context.Context, ec2Client *ec2.Client) (*string, error) {
	vpcCreateResponse, err := ec2Client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("172.31.0.0/16"),
	})
	if err != nil {
		return nil, err
	}
	vpcID := vpcCreateResponse.Vpc.VpcId

	err = tagResource(ctx, ec2Client, vpcID)
	if err != nil {
		return nil, err
	}

	subnetResponse, err := ec2Client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		CidrBlock: aws.String("172.31.0.0/16"),
		VpcId:     vpcID,
	})
	if err != nil {
		return nil, err
	}
	subnetID := subnetResponse.Subnet.SubnetId

	err = tagResource(ctx, ec2Client, subnetID)
	if err != nil {
		return nil, err
	}

	igResponse, err := ec2Client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		return nil, err
	}
	igID := igResponse.InternetGateway.InternetGatewayId

	err = tagResource(ctx, ec2Client, igID)
	if err != nil {
		return nil, err
	}

	_, err = ec2Client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: igID,
		VpcId:             vpcID,
	})
	if err != nil {
		return nil, err
	}

	routeTableResponse, err := ec2Client.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId: vpcID,
	})
	if err != nil {
		return nil, err
	}

	routeTableID := routeTableResponse.RouteTable.RouteTableId

	err = tagResource(ctx, ec2Client, routeTableID)
	if err != nil {
		return nil, err
	}

	_, err = ec2Client.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
		RouteTableId: routeTableID,
		SubnetId:     subnetID,
	})
	if err != nil {
		return nil, err
	}

	_, err = ec2Client.CreateRoute(ctx, &ec2.CreateRouteInput{
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            igID,
		RouteTableId:         routeTableID,
	})
	if err != nil {
		return nil, err
	}

	return subnetID, nil
}

// tagResource tries to tag an EC2 resource in a loop to workaround EC2 eventual consistency
func tagResource(ctx context.Context, ec2Client *ec2.Client, resourceID *string) error {
	fmt.Printf("Tagging: %s\n", aws.ToString(resourceID))
	return retry(func() error {
		_, err := ec2Client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{aws.ToString(resourceID)},
			Tags: []ec2types.Tag{{
				Key:   aws.String("Name"),
				Value: aws.String("vk-aws-e2e-test"),
			}},
		})

		return err
	})
}

// deleteVpc deletes all resources of the created VPC by enumarating all tagged
// resources and deleting them with multiple retries to cope with EC2 eventual
// consistency
func deleteVpc(ctx context.Context, ec2Client *ec2.Client) error {
	// Remove any routing tables
	retry(func() error {
		resourceIDs, err := findResourceByTag(ctx, ec2Client, "route-table")
		if err != nil {
			return err
		}

		describeResponse, err := ec2Client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
			RouteTableIds: resourceIDs,
		})
		if err != nil {
			return err
		}

		for _, routeTable := range describeResponse.RouteTables {
			for _, association := range routeTable.Associations {
				_, err = ec2Client.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{
					AssociationId: association.RouteTableAssociationId,
				})

				if err != nil {
					return err
				}
			}

			_, err = ec2Client.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{
				RouteTableId: routeTable.RouteTableId,
			})

			if err != nil {
				return err
			}
		}

		return nil
	})

	// Remove associatated internet gateways
	retry(func() error {
		resourceIDs, err := findResourceByTag(ctx, ec2Client, "internet-gateway")
		if err != nil {
			return err
		}

		describeResponse, err := ec2Client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
			InternetGatewayIds: resourceIDs,
		})
		if err != nil {
			return err
		}

		for _, internetGateway := range describeResponse.InternetGateways {
			for _, attachment := range internetGateway.Attachments {
				_, err = ec2Client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
					InternetGatewayId: internetGateway.InternetGatewayId,
					VpcId:             attachment.VpcId,
				})

				if err != nil {
					return err
				}
			}

			_, err = ec2Client.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
				InternetGatewayId: internetGateway.InternetGatewayId,
			})

			if err != nil {
				return err
			}
		}

		return nil
	})

	// Remove subnets
	retry(func() error {
		resourceIDs, err := findResourceByTag(ctx, ec2Client, "subnet")
		if err != nil {
			return err
		}

		for _, resourceID := range resourceIDs {
			_, err = ec2Client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
				SubnetId: aws.String(resourceID),
			})

			if err != nil {
				return err
			}
		}

		return nil
	})

	// Remove the VPC itself
	retry(func() error {
		resourceIDs, err := findResourceByTag(ctx, ec2Client, "vpc")
		if err != nil {
			return err
		}

		for _, resourceID := range resourceIDs {
			_, err = ec2Client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
				VpcId: aws.String(resourceID),
			})

			if err != nil {
				return err
			}
		}

		return nil
	})

	return nil
}

// findResourceByTag finds EC2 resources by a tag
func findResourceByTag(ctx context.Context, ec2Client *ec2.Client, resourceType string) ([]string, error) {
	describeResponse, err := ec2Client.DescribeTags(ctx, &ec2.DescribeTagsInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("key"),
				Values: []string{"Name"},
			},
			{
				Name:   aws.String("value"),
				Values: []string{"vk-aws-e2e-test"},
			},
			{
				Name:   aws.String("resource-type"),
				Values: []string{resourceType},
			},
		},
	})

	if err != nil {
		return nil, err
	}

	resourceIDs := make([]string, len(describeResponse.Tags))
	for i, tag := range describeResponse.Tags {
		resourceIDs[i] = aws.ToString(tag.ResourceId)
	}

	return resourceIDs, nil
}

type fn func() error

// retry retries an action up to 10 times
func retry(action fn) error {
	attempts := 10
	sleep := time.Second * 10

	for {
		if attempts == 0 {
			return fmt.Errorf("action failed, maximum attempts reached")
		}
		attempts--

		err := action()
		if err == nil {
			return nil
		}

		fmt.Printf("action failed, err: %s retrying...\n", err)

		time.Sleep(sleep)
	}
}
