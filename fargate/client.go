package fargate

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

// ecsAPI is the part of the Amazon ECS API the provider calls.
type ecsAPI interface {
	CreateCluster(context.Context, *ecs.CreateClusterInput, ...func(*ecs.Options)) (*ecs.CreateClusterOutput, error)
	DescribeClusters(context.Context, *ecs.DescribeClustersInput, ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error)
	RegisterTaskDefinition(context.Context, *ecs.RegisterTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
	DescribeTaskDefinition(context.Context, *ecs.DescribeTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
	DeregisterTaskDefinition(context.Context, *ecs.DeregisterTaskDefinitionInput, ...func(*ecs.Options)) (*ecs.DeregisterTaskDefinitionOutput, error)
	RunTask(context.Context, *ecs.RunTaskInput, ...func(*ecs.Options)) (*ecs.RunTaskOutput, error)
	StopTask(context.Context, *ecs.StopTaskInput, ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
	DescribeTasks(context.Context, *ecs.DescribeTasksInput, ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
	ListTasks(context.Context, *ecs.ListTasksInput, ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
}

// logsAPI is the part of the Amazon CloudWatch Logs API the provider calls.
type logsAPI interface {
	DescribeLogStreams(context.Context, *cloudwatchlogs.DescribeLogStreamsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogStreamsOutput, error)
	GetLogEvents(context.Context, *cloudwatchlogs.GetLogEventsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
}

// Client communicates with the regional AWS Fargate service.
type Client struct {
	region  string
	api     ecsAPI
	logsapi logsAPI
}

var client *Client

// newClient creates a client for the given Region with the AWS SDK's default configuration chain: credentials from the
// environment, the shared configuration and credentials files, SSO, web identity (such as IAM roles for service
// accounts), the container or the instance metadata service; and endpoints from AWS_ENDPOINT_URL and
// AWS_ENDPOINT_URL_<SERVICE> or the shared configuration file, as every current AWS SDK reads them. An empty region
// leaves the Region to that chain (AWS_REGION, or the profile's region).
func newClient(ctx context.Context, region string) (*Client, error) {
	options := []func(*config.LoadOptions) error{}
	if region != "" {
		options = append(options, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("no AWS Region is set: set Region in the provider configuration, or AWS_REGION")
	}

	log.Printf("Created Fargate service client for region %s.", cfg.Region)

	return &Client{
		region:  cfg.Region,
		api:     ecs.NewFromConfig(cfg),
		logsapi: cloudwatchlogs.NewFromConfig(cfg),
	}, nil
}
