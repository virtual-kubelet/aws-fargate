package fargate

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

const (
	// Prefixes for objects created in Fargate.
	taskDefFamilyPrefix = "vk-podspec"
	taskTagPrefix       = "vk-pod"

	// Task status strings.
	taskStatusProvisioning = "PROVISIONING"
	taskStatusPending      = "PENDING"
	taskStatusRunning      = "RUNNING"
	taskStatusStopped      = "STOPPED"

	// Task attachment types.
	taskAttachmentENI                   = "ElasticNetworkInterface"
	taskAttachmentENIPrivateIPv4Address = "privateIPv4Address"

	// Reason used for task state changes.
	taskGenericReason = "Initiated by user"

	// Annotation to configure the task role.
	taskRoleAnnotation = "iam.amazonaws.com/role"
)

// Pod is the representation of a Kubernetes pod in Fargate.
type Pod struct {
	// Kubernetes pod properties.
	namespace string
	name      string
	uid       k8sTypes.UID

	// Fargate task properties.
	cluster         *Cluster
	taskDefArn      string
	taskArn         string
	taskRoleArn     string
	taskStatus      string
	taskRefreshTime time.Time
	taskCPU         int64
	taskMemory      int64
	containers      map[string]*container
}

// NewPod creates a new Kubernetes pod on Fargate.
func NewPod(ctx context.Context, cluster *Cluster, pod *corev1.Pod) (*Pod, error) {
	// Initialize the pod.
	fgPod := &Pod{
		namespace:  pod.Namespace,
		name:       pod.Name,
		uid:        pod.UID,
		cluster:    cluster,
		containers: make(map[string]*container),
	}

	tag := fgPod.buildTaskDefinitionTag()

	// Create a task definition matching the pod spec.
	taskDef := &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(tag),
		RequiresCompatibilities: []types.Compatibility{types.CompatibilityFargate},
		NetworkMode:             types.NetworkModeAwsvpc,
		ContainerDefinitions:    []types.ContainerDefinition{},
	}

	// For each container in the pod...
	for _, containerSpec := range pod.Spec.Containers {
		// Create a container definition.
		cntr, err := newContainer(&containerSpec)
		if err != nil {
			return nil, err
		}

		// Configure container logs to be sent to CloudWatch Logs if enabled.
		if cluster.cloudWatchLogGroupName != "" {
			cntr.configureLogs(cluster.region, cluster.cloudWatchLogGroupName, tag)
		}

		// Add the container's resource requirements to its pod's total resource requirements.
		fgPod.taskCPU += int64(cntr.definition.Cpu)
		fgPod.taskMemory += int64(aws.ToInt32(cntr.definition.Memory))

		// Insert the container to its pod.
		fgPod.containers[containerSpec.Name] = cntr

		// Insert container definition to the task definition.
		taskDef.ContainerDefinitions = append(taskDef.ContainerDefinitions, cntr.definition)
	}

	// Set task resource limits.
	err := fgPod.mapTaskSize()
	if err != nil {
		return nil, err
	}

	taskDef.Cpu = aws.String(strconv.Itoa(int(fgPod.taskCPU)))
	taskDef.Memory = aws.String(strconv.Itoa(int(fgPod.taskMemory)))

	// Set a custom task execution IAM role if configured.
	if cluster.executionRoleArn != "" {
		taskDef.ExecutionRoleArn = aws.String(cluster.executionRoleArn)
	}

	// Set a custom task IAM role if configured.
	if val, ok := pod.Annotations[taskRoleAnnotation]; ok {
		taskDef.TaskRoleArn = aws.String(val)
		fgPod.taskRoleArn = val
	}

	// Register the task definition with Fargate.
	log.Printf("RegisterTaskDefinition input:%+v", taskDef)
	output, err := client.api.RegisterTaskDefinition(ctx, taskDef)
	if err != nil {
		err = fmt.Errorf("failed to register task definition: %v", err)
		return nil, err
	}

	log.Printf("RegisterTaskDefinition output:%+v", output)

	// Save the registered task definition ARN.
	fgPod.taskDefArn = aws.ToString(output.TaskDefinition.TaskDefinitionArn)

	if cluster != nil {
		cluster.InsertPod(fgPod, tag)
	}

	return fgPod, nil
}

// NewPodFromTag creates a new pod identified by a tag.
func NewPodFromTag(cluster *Cluster, tag string) (*Pod, error) {
	data := strings.Split(tag, "_")

	if len(data) < 4 ||
		data[0] != taskDefFamilyPrefix ||
		data[1] != cluster.name {
		return nil, fmt.Errorf("invalid tag")
	}

	pod := &Pod{
		namespace:  data[2],
		name:       data[3],
		cluster:    cluster,
		containers: make(map[string]*container),
	}

	return pod, nil
}

// Start deploys and runs a Kubernetes pod on Fargate.
func (pod *Pod) Start(ctx context.Context) error {
	// Pods always get an ENI with a private IPv4 address in customer subnet.
	// Assign a public IPv4 address to the ENI only if requested.
	assignPublicIPAddress := types.AssignPublicIpDisabled
	if pod.cluster.assignPublicIPv4Address {
		assignPublicIPAddress = types.AssignPublicIpEnabled
	}

	// Start the task.
	runTaskInput := &ecs.RunTaskInput{
		Cluster:    aws.String(pod.cluster.name),
		Count:      aws.Int32(1),
		LaunchType: types.LaunchTypeFargate,
		NetworkConfiguration: &types.NetworkConfiguration{
			AwsvpcConfiguration: &types.AwsVpcConfiguration{
				AssignPublicIp: assignPublicIPAddress,
				SecurityGroups: pod.cluster.securityGroups,
				Subnets:        pod.cluster.subnets,
			},
		},
		PlatformVersion: aws.String(pod.cluster.platformVersion),
		StartedBy:       aws.String(pod.buildTaskTag()),
		TaskDefinition:  aws.String(pod.taskDefArn),
	}

	log.Printf("RunTask input:%+v", runTaskInput)
	runTaskOutput, err := client.api.RunTask(ctx, runTaskInput)
	if err == nil && len(runTaskOutput.Tasks) == 0 {
		err = fmt.Errorf("no task in the answer")
		if len(runTaskOutput.Failures) != 0 {
			err = fmt.Errorf("reason: %s", aws.ToString(runTaskOutput.Failures[0].Reason))
		}
	}
	if err != nil {
		err = fmt.Errorf("failed to run task: %v", err)
		return err
	}

	log.Printf("RunTask output:%+v", runTaskOutput)
	// Save the task ARN.
	pod.taskArn = aws.ToString(runTaskOutput.Tasks[0].TaskArn)

	return nil
}

// Stop stops a running Kubernetes pod on Fargate.
func (pod *Pod) Stop(ctx context.Context) error {
	// Stop the task.
	stopTaskInput := &ecs.StopTaskInput{
		Cluster: aws.String(pod.cluster.name),
		Reason:  aws.String(taskGenericReason),
		Task:    aws.String(pod.taskArn),
	}

	log.Printf("StopTask input:%+v", stopTaskInput)
	stopTaskOutput, err := client.api.StopTask(ctx, stopTaskInput)
	log.Printf("StopTask err:%+v output:%+v", err, stopTaskOutput)
	if err != nil {
		err = fmt.Errorf("failed to stop task: %v", err)
		return err
	}

	// Deregister the task definition.
	_, err = client.api.DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String(pod.taskDefArn),
	})
	if err != nil {
		log.Printf("Failed to deregister task definition: %v", err)
	}

	// Remove the pod from its cluster.
	if pod.cluster != nil {
		pod.cluster.RemovePod(pod.buildTaskDefinitionTag())
	}

	return nil
}

// GetSpec returns the specification of a Kubernetes pod on Fargate.
func (pod *Pod) GetSpec(ctx context.Context) (*corev1.Pod, error) {
	task, err := pod.describe(ctx)
	if err != nil {
		return nil, err
	}

	return pod.getSpec(task)
}

// GetStatus returns the status of a Kubernetes pod on Fargate.
func (pod *Pod) GetStatus(ctx context.Context) corev1.PodStatus {
	task, err := pod.describe(ctx)
	if err != nil {
		return corev1.PodStatus{Phase: corev1.PodUnknown}
	}

	return pod.getStatus(task)
}

// BuildTaskDefinitionTag returns the task definition tag for this pod.
func (pod *Pod) buildTaskDefinitionTag() string {
	return buildTaskDefinitionTag(pod.cluster.name, pod.namespace, pod.name)
}

// buildTaskDefinitionTag builds a task definition tag from its components.
func buildTaskDefinitionTag(clusterName string, namespace string, name string) string {
	// vk-podspec_cluster_namespacae_podname
	return fmt.Sprintf("%s_%s_%s_%s", taskDefFamilyPrefix, clusterName, namespace, name)
}

// BuildTaskTag returns the pod's task tag, used for mapping a task back to its pod.
func (pod *Pod) buildTaskTag() string {
	return fmt.Sprintf("%s", pod.uid)
}

// mapTaskSize maps Kubernetes pod resource requirements to a Fargate task size.
func (pod *Pod) mapTaskSize() error {
	//
	// Kubernetes pods do not have explicit resource requirements; their containers do. Pod resource
	// requirements are the sum of the pod's containers' requirements.
	//
	// Fargate tasks have explicit CPU and memory limits. Both are required and specify the maximum
	// amount of resources for the task. The limits must match a task size on taskSizeTable.
	//
	var cpu int64
	var memory int64

	// Find the smallest Fargate task size that can satisfy the total resource request.
	for _, row := range taskSizeTable {
		if pod.taskCPU <= row.cpu {
			for mem := row.memory.min; mem <= row.memory.max; mem += row.memory.inc {
				if pod.taskMemory <= mem/MiB {
					cpu = row.cpu
					memory = mem / MiB
					break
				}
			}

			if cpu != 0 {
				break
			}
		}
	}

	log.Printf("Mapped resource requirements (cpu:%v, memory:%v) to task size (cpu:%v, memory:%v)",
		pod.taskCPU, pod.taskMemory, cpu, memory)

	// Fail if the resource requirements cannot be satisfied by any Fargate task size.
	if cpu == 0 {
		return fmt.Errorf("resource requirements (cpu:%v, memory:%v) are too high",
			pod.taskCPU, pod.taskMemory)
	}

	// Fargate task CPU size is specified in vCPU/1024s and memory size is specified in MiBs.
	pod.taskCPU = cpu
	pod.taskMemory = memory

	return nil
}

// Describe retrieves the status of a Kubernetes pod from Fargate.
func (pod *Pod) describe(ctx context.Context) (*types.Task, error) {
	// Describe the task.
	describeTasksInput := &ecs.DescribeTasksInput{
		Cluster: aws.String(pod.cluster.name),
		Tasks:   []string{pod.taskArn},
	}

	describeTasksOutput, err := client.api.DescribeTasks(ctx, describeTasksInput)
	if err == nil && len(describeTasksOutput.Tasks) == 0 {
		err = fmt.Errorf("no task in the answer")
		if len(describeTasksOutput.Failures) != 0 {
			err = fmt.Errorf("reason: %s", aws.ToString(describeTasksOutput.Failures[0].Reason))
		}
	}
	if err != nil {
		err = fmt.Errorf("failed to describe task: %v", err)
		return nil, err
	}

	task := describeTasksOutput.Tasks[0]

	pod.taskStatus = aws.ToString(task.LastStatus)
	pod.taskRefreshTime = time.Now()

	return &task, nil
}

// GetSpec returns the specification of a Kubernetes pod on Fargate.
func (pod *Pod) getSpec(task *types.Task) (*corev1.Pod, error) {
	containers := make([]corev1.Container, 0, len(task.Containers))

	for _, c := range task.Containers {
		cntrDef := pod.containers[aws.ToString(c.Name)].definition

		cntr := corev1.Container{
			Name:    aws.ToString(c.Name),
			Image:   aws.ToString(cntrDef.Image),
			Command: cntrDef.EntryPoint,
			Args:    cntrDef.Command,
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", cntrDef.Cpu)),
					corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", aws.ToInt32(cntrDef.Memory))),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", cntrDef.Cpu)),
					corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", aws.ToInt32(cntrDef.MemoryReservation))),
				},
			},
			Ports: make([]corev1.ContainerPort, 0, len(cntrDef.PortMappings)),
			Env:   make([]corev1.EnvVar, 0, len(cntrDef.Environment)),
		}

		if cntrDef.WorkingDirectory != nil {
			cntr.WorkingDir = aws.ToString(cntrDef.WorkingDirectory)
		}

		for _, mapping := range cntrDef.PortMappings {
			cntr.Ports = append(cntr.Ports, corev1.ContainerPort{
				ContainerPort: aws.ToInt32(mapping.ContainerPort),
				HostPort:      aws.ToInt32(mapping.HostPort),
				Protocol:      corev1.ProtocolTCP,
			})
		}

		for _, env := range cntrDef.Environment {
			cntr.Env = append(cntr.Env, corev1.EnvVar{
				Name:  aws.ToString(env.Name),
				Value: aws.ToString(env.Value),
			})
		}

		containers = append(containers, cntr)
	}

	annotations := make(map[string]string)

	if pod.taskRoleArn != "" {
		annotations[taskRoleAnnotation] = pod.taskRoleArn
	}

	podSpec := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   pod.namespace,
			Name:        pod.name,
			UID:         pod.uid,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			NodeName:   pod.cluster.nodeName,
			Volumes:    []corev1.Volume{},
			Containers: containers,
		},
		Status: pod.getStatus(task),
	}

	return &podSpec, nil
}

// GetStatus returns the status of a Kubernetes pod on Fargate.
func (pod *Pod) getStatus(task *types.Task) corev1.PodStatus {
	// Translate task status to pod phase.
	phase := corev1.PodUnknown

	switch pod.taskStatus {
	case taskStatusProvisioning:
		phase = corev1.PodPending
	case taskStatusPending:
		phase = corev1.PodPending
	case taskStatusRunning:
		phase = corev1.PodRunning
	case taskStatusStopped:
		phase = corev1.PodSucceeded
	}

	// Set pod conditions based on task's last known status.
	isScheduled := corev1.ConditionFalse
	isInitialized := corev1.ConditionFalse
	isReady := corev1.ConditionFalse

	switch pod.taskStatus {
	case taskStatusProvisioning:
		isScheduled = corev1.ConditionTrue
	case taskStatusPending:
		isScheduled = corev1.ConditionTrue
	case taskStatusRunning:
		isScheduled = corev1.ConditionTrue
		isInitialized = corev1.ConditionTrue
		isReady = corev1.ConditionTrue
	case taskStatusStopped:
		isScheduled = corev1.ConditionTrue
		isInitialized = corev1.ConditionTrue
		isReady = corev1.ConditionTrue
	}

	conditions := []corev1.PodCondition{
		corev1.PodCondition{
			Type:   corev1.PodScheduled,
			Status: isScheduled,
		},
		corev1.PodCondition{
			Type:   corev1.PodInitialized,
			Status: isInitialized,
		},
		corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: isReady,
		},
	}

	// Set the pod start time as the task creation time.
	var startTime metav1.Time
	if task.CreatedAt != nil {
		startTime = metav1.NewTime(*task.CreatedAt)
	}

	// Set the pod IP address from the task ENI information.
	privateIPv4Address := ""
	for _, attachment := range task.Attachments {
		if aws.ToString(attachment.Type) == taskAttachmentENI {
			for _, detail := range attachment.Details {
				if aws.ToString(detail.Name) == taskAttachmentENIPrivateIPv4Address {
					privateIPv4Address = aws.ToString(detail.Value)
				}
			}
		}
	}

	// Get statuses from all containers in this pod.
	containerStatuses := make([]corev1.ContainerStatus, 0, len(task.Containers))
	for _, cntr := range task.Containers {
		containerStatuses = append(containerStatuses, pod.containers[aws.ToString(cntr.Name)].getStatus(cntr))
	}

	// Build the pod status structure to be reported.
	status := corev1.PodStatus{
		Phase:                 phase,
		Conditions:            conditions,
		Message:               "",
		Reason:                "",
		HostIP:                privateIPv4Address,
		PodIP:                 privateIPv4Address,
		StartTime:             &startTime,
		InitContainerStatuses: nil,
		ContainerStatuses:     containerStatuses,
		QOSClass:              corev1.PodQOSBestEffort,
	}

	return status
}
