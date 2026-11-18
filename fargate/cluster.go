package fargate

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/virtual-kubelet/virtual-kubelet/errdefs"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

const (
	clusterFailureReasonMissing = "MISSING"
)

// ClusterConfig contains a Fargate cluster's configurable parameters.
type ClusterConfig struct {
	Region                  string
	Name                    string
	NodeName                string
	Subnets                 []string
	SecurityGroups          []string
	AssignPublicIPv4Address bool
	ExecutionRoleArn        string
	CloudWatchLogGroupName  string
	PlatformVersion         string
}

// Cluster represents a Fargate cluster.
type Cluster struct {
	region                  string
	name                    string
	nodeName                string
	arn                     string
	subnets                 []string
	securityGroups          []string
	assignPublicIPv4Address bool
	executionRoleArn        string
	cloudWatchLogGroupName  string
	platformVersion         string
	pods                    map[string]*Pod
	sync.RWMutex
}

// NewCluster creates a new Cluster object.
func NewCluster(ctx context.Context, config *ClusterConfig) (*Cluster, error) {
	var err error

	// Cluster name cannot contain '_' as it is used as a separator in task tags.
	if strings.Contains(config.Name, "_") {
		return nil, fmt.Errorf("cluster name should not contain the '_' character")
	}

	// Create the client to the regional Fargate service.
	client, err = newClient(ctx, config.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to create Fargate client: %v", err)
	}

	// Initialize the cluster.
	cluster := &Cluster{
		region:                  client.region,
		name:                    config.Name,
		nodeName:                config.NodeName,
		subnets:                 config.Subnets,
		securityGroups:          config.SecurityGroups,
		assignPublicIPv4Address: config.AssignPublicIPv4Address,
		executionRoleArn:        config.ExecutionRoleArn,
		cloudWatchLogGroupName:  config.CloudWatchLogGroupName,
		platformVersion:         config.PlatformVersion,
		pods:                    make(map[string]*Pod),
	}

	// If a node name is not specified, use the Fargate cluster name.
	if cluster.nodeName == "" {
		cluster.nodeName = cluster.name
	}

	// Check if the cluster already exists.
	err = cluster.describe(ctx)
	if err != nil && !strings.Contains(err.Error(), clusterFailureReasonMissing) {
		return nil, err
	}

	// If not, try to create it.
	// This might fail if the principal doesn't have the necessary permission.
	if cluster.arn == "" {
		err = cluster.create(ctx)
		if err != nil {
			return nil, err
		}
	}

	// Load existing pod state from Fargate to the local cache.
	err = cluster.loadPodState(ctx)
	if err != nil {
		return nil, err
	}

	return cluster, nil
}

// Create creates a new Fargate cluster.
func (c *Cluster) create(ctx context.Context) error {
	input := &ecs.CreateClusterInput{
		ClusterName: aws.String(c.name),
	}

	log.Printf("Creating Fargate cluster %s in region %s", c.name, c.region)

	output, err := client.api.CreateCluster(ctx, input)
	if err != nil {
		err = fmt.Errorf("failed to create cluster: %v", err)
		log.Println(err)
		return err
	}

	c.arn = aws.ToString(output.Cluster.ClusterArn)
	log.Printf("Created Fargate cluster %s in region %s", c.name, c.region)

	return nil
}

// Describe loads information from an existing Fargate cluster.
func (c *Cluster) describe(ctx context.Context) error {
	input := &ecs.DescribeClustersInput{
		Clusters: []string{c.name},
	}

	log.Printf("Looking for Fargate cluster %s in region %s.", c.name, c.region)

	output, err := client.api.DescribeClusters(ctx, input)
	if err == nil && len(output.Clusters) == 0 {
		err = fmt.Errorf("no cluster in the answer")
		if len(output.Failures) > 0 {
			err = fmt.Errorf("reason: %s", aws.ToString(output.Failures[0].Reason))
		}
	}
	if err != nil {
		err = fmt.Errorf("failed to describe cluster: %v", err)
		log.Println(err)
		return err
	}

	log.Printf("Found Fargate cluster %s in region %s.", c.name, c.region)
	c.arn = aws.ToString(output.Clusters[0].ClusterArn)

	return nil
}

// LoadPodState rebuilds pod and container objects in this cluster by loading existing tasks from
// Fargate. This is done during startup and whenever the local state is suspected to be out of sync
// with the actual state in Fargate. Caching state locally minimizes the number of service calls.
func (c *Cluster) loadPodState(ctx context.Context) error {
	log.Printf("Loading pod state from cluster %s.", c.name)

	taskArns := make([]string, 0)

	// Get a list of all Fargate tasks running on this cluster.
	paginator := ecs.NewListTasksPaginator(client.api, &ecs.ListTasksInput{
		Cluster:       aws.String(c.name),
		DesiredStatus: types.DesiredStatusRunning,
		LaunchType:    types.LaunchTypeFargate,
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			err := fmt.Errorf("failed to load pod state: %v", err)
			log.Println(err)
			return err
		}
		taskArns = append(taskArns, page.TaskArns...)
	}

	log.Printf("Found %d tasks on cluster %s.", len(taskArns), c.name)

	pods := make(map[string]*Pod)

	// For each task running on this Fargate cluster...
	for _, taskArn := range taskArns {
		// Describe the task.
		describeTasksOutput, err := client.api.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: aws.String(c.name),
			Tasks:   []string{taskArn},
		})

		if err != nil || len(describeTasksOutput.Tasks) != 1 {
			log.Printf("Failed to describe task %s. Skipping.", taskArn)
			continue
		}

		task := describeTasksOutput.Tasks[0]

		// Describe the task definition.
		describeTaskDefinitionOutput, err := client.api.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
			TaskDefinition: task.TaskDefinitionArn,
		})

		if err != nil {
			log.Printf("Failed to describe task definition %s. Skipping.", aws.ToString(task.TaskDefinitionArn))
			continue
		}

		taskDef := describeTaskDefinitionOutput.TaskDefinition

		// A pod's tag is stored in its task definition's Family field.
		tag := aws.ToString(taskDef.Family)

		// Rebuild the pod object.
		// Not all tasks are necessarily pods. Skip tasks that do not have a valid tag.
		pod, err := NewPodFromTag(c, tag)
		if err != nil {
			log.Printf("Skipping unknown task %s: %v", taskArn, err)
			continue
		}

		pod.uid = k8sTypes.UID(aws.ToString(task.StartedBy))
		pod.taskDefArn = aws.ToString(task.TaskDefinitionArn)
		pod.taskArn = aws.ToString(task.TaskArn)
		if taskDef.TaskRoleArn != nil {
			pod.taskRoleArn = aws.ToString(taskDef.TaskRoleArn)
		}
		pod.taskStatus = aws.ToString(task.LastStatus)
		pod.taskRefreshTime = time.Now()

		// Rebuild the container objects.
		for _, cntrDef := range taskDef.ContainerDefinitions {
			cntr, _ := newContainerFromDefinition(cntrDef, task.CreatedAt)

			pod.taskCPU += int64(cntr.definition.Cpu)
			pod.taskMemory += int64(aws.ToInt32(cntr.definition.Memory))
			pod.containers[aws.ToString(cntrDef.Name)] = cntr

			log.Printf("Found pod %s/%s on cluster %s.", pod.namespace, pod.name, c.name)
		}

		pods[tag] = pod
	}

	// Update local state.
	c.Lock()
	c.pods = pods
	c.Unlock()

	return nil
}

// GetPod returns a Kubernetes pod deployed on this cluster.
func (c *Cluster) GetPod(namespace string, name string) (*Pod, error) {
	c.RLock()
	defer c.RUnlock()

	tag := buildTaskDefinitionTag(c.name, namespace, name)
	pod, ok := c.pods[tag]
	if !ok {
		return nil, errdefs.NotFoundf("pod %s/%s is not found", namespace, name)
	}

	return pod, nil
}

// GetPods returns all Kubernetes pods deployed on this cluster.
func (c *Cluster) GetPods() ([]*Pod, error) {
	c.RLock()
	defer c.RUnlock()

	pods := make([]*Pod, 0, len(c.pods))

	for _, pod := range c.pods {
		pods = append(pods, pod)
	}

	return pods, nil
}

// InsertPod inserts a Kubernetes pod to this cluster.
func (c *Cluster) InsertPod(pod *Pod, tag string) {
	c.Lock()
	defer c.Unlock()

	c.pods[tag] = pod
}

// RemovePod removes a Kubernetes pod from this cluster.
func (c *Cluster) RemovePod(tag string) {
	c.Lock()
	defer c.Unlock()

	delete(c.pods, tag)
}

// GetContainerLogs returns the logs of a container from this cluster.
func (c *Cluster) GetContainerLogs(ctx context.Context, namespace, podName, containerName string, opts api.ContainerLogOpts) (io.ReadCloser, error) {
	if c.cloudWatchLogGroupName == "" {
		return nil, fmt.Errorf("logs not configured, please specify a \"CloudWatchLogGroupName\"")
	}

	prefix := fmt.Sprintf("%s_%s", buildTaskDefinitionTag(c.name, namespace, podName), containerName)
	describeResult, err := client.logsapi.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName:        aws.String(c.cloudWatchLogGroupName),
		LogStreamNamePrefix: aws.String(prefix),
	})
	if err != nil {
		return nil, err
	}

	// Nothing logged yet.
	if len(describeResult.LogStreams) == 0 {
		return nil, nil
	}

	logs := ""

	input := &cloudwatchlogs.GetLogEventsInput{
		Limit:         aws.Int32(int32(opts.Tail)),
		LogGroupName:  aws.String(c.cloudWatchLogGroupName),
		LogStreamName: describeResult.LogStreams[0].LogStreamName,
	}
	for {
		page, err := client.logsapi.GetLogEvents(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, event := range page.Events {
			logs += aws.ToString(event.Message)
			logs += "\n"
		}

		// Stop as soon as a page returns no further events, or the service returns the token it was given.
		if len(page.Events) == 0 || page.NextForwardToken == nil || aws.ToString(page.NextForwardToken) == aws.ToString(input.NextToken) {
			break
		}
		input.NextToken = page.NextForwardToken
	}

	return ioutil.NopCloser(strings.NewReader(logs)), nil
}
