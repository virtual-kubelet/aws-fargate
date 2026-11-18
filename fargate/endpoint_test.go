package fargate

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The values below are AWS's documentation examples.
const (
	exampleAccessKeyID     = "AKIAIOSFODNN7EXAMPLE"
	exampleSecretAccessKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	exampleAccount         = "123456789012"
	exampleSubnet          = "subnet-12345678"
	exampleSecurityGroup   = "sg-12345678"
	// A Region name no SDK release ships a table entry for, as a private or newer endpoint can have.
	exampleRegion = "example-region-1"
)

// call is one request a fake endpoint received.
type call struct {
	target string                 // the X-Amz-Target operation, without the service prefix
	region string                 // the Region of the request's SigV4 credential scope
	signer string                 // the service of the request's SigV4 credential scope
	body   map[string]interface{} // the JSON request
}

// fakeEndpoint answers AWS JSON 1.1 requests from a table of operations and records each request.
type fakeEndpoint struct {
	sync.Mutex
	server  *httptest.Server
	calls   []call
	answers map[string]func(body map[string]interface{}) (int, interface{})
}

var credentialScope = regexp.MustCompile(`Credential=[^/]+/[0-9]{8}/([^/]+)/([^/]+)/aws4_request`)

func newFakeEndpoint(t *testing.T) *fakeEndpoint {
	f := &fakeEndpoint{answers: map[string]func(map[string]interface{}) (int, interface{}){}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		target = target[strings.LastIndex(target, ".")+1:]
		raw, _ := ioutil.ReadAll(r.Body)
		body := map[string]interface{}{}
		_ = json.Unmarshal(raw, &body)
		c := call{target: target, body: body}
		if m := credentialScope.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
			c.region, c.signer = m[1], m[2]
		}
		f.Lock()
		f.calls = append(f.calls, c)
		answer, ok := f.answers[target]
		f.Unlock()

		status, document := http.StatusBadRequest, interface{}(map[string]string{
			"__type": "UnknownOperationException", "message": "no answer for " + target})
		if ok {
			status, document = answer(body)
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeEndpoint) answer(target string, answer func(body map[string]interface{}) (int, interface{})) {
	f.Lock()
	defer f.Unlock()
	f.answers[target] = answer
}

func (f *fakeEndpoint) targets() []string {
	f.Lock()
	defer f.Unlock()
	targets := []string{}
	for _, c := range f.calls {
		targets = append(targets, c.target)
	}
	return targets
}

func (f *fakeEndpoint) last(target string) call {
	f.Lock()
	defer f.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].target == target {
			return f.calls[i]
		}
	}
	return call{}
}

// isolate gives a test the documentation example credentials and nothing else of the environment it runs in: no
// profile, no shared files, no instance metadata, no Region, no endpoint, and one attempt per call.
func isolate(t *testing.T) {
	none := filepath.Join(t.TempDir(), "none")
	t.Setenv("AWS_ACCESS_KEY_ID", exampleAccessKeyID)
	t.Setenv("AWS_SECRET_ACCESS_KEY", exampleSecretAccessKey)
	t.Setenv("AWS_CONFIG_FILE", none)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", none)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	for _, name := range []string{"AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION",
		"AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_ECS", "AWS_ENDPOINT_URL_CLOUDWATCH_LOGS", "AWS_IGNORE_CONFIGURED_ENDPOINT_URLS",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func clusterArn(name string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/%s", exampleRegion, exampleAccount, name)
}

// answerNewCluster makes the fake answer the three calls NewCluster makes for a cluster that does not exist yet.
func answerNewCluster(f *fakeEndpoint) {
	f.answer("DescribeClusters", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"clusters": []interface{}{}, "failures": []interface{}{
			map[string]interface{}{"arn": clusterArn("test"), "reason": "MISSING"}}}
	})
	f.answer("CreateCluster", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"cluster": map[string]interface{}{
			"clusterArn": clusterArn("test"), "clusterName": body["clusterName"], "status": "ACTIVE"}}
	})
	f.answer("ListTasks", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"taskArns": []interface{}{}}
	})
}

func testClusterConfig() *ClusterConfig {
	return &ClusterConfig{
		Region:          exampleRegion,
		Name:            "test",
		Subnets:         []string{exampleSubnet},
		SecurityGroups:  []string{exampleSecurityGroup},
		PlatformVersion: "LATEST",
	}
}

// TestClientUsesEndpointURLAndAnyRegion: the client reaches the endpoint AWS_ENDPOINT_URL names, signs for the Region
// it was given whatever its name, and NewCluster creates a cluster that does not exist.
func TestClientUsesEndpointURLAndAnyRegion(t *testing.T) {
	isolate(t)
	f := newFakeEndpoint(t)
	answerNewCluster(f)
	t.Setenv("AWS_ENDPOINT_URL", f.server.URL)

	cluster, err := NewCluster(context.Background(), testClusterConfig())
	assert.NilError(t, err)

	assert.DeepEqual(t, f.targets(), []string{"DescribeClusters", "CreateCluster", "ListTasks"})
	created := f.last("CreateCluster")
	assert.Check(t, is.Equal(created.body["clusterName"], "test"))
	assert.Check(t, is.Equal(created.region, exampleRegion))
	assert.Check(t, is.Equal(created.signer, "ecs"))
	assert.Check(t, is.Equal(cluster.arn, clusterArn("test")))
	assert.Check(t, is.Equal(cluster.region, exampleRegion))
}

// TestClientRegionFromEnvironment: with no Region in the provider configuration, the SDK's own Region is used.
func TestClientRegionFromEnvironment(t *testing.T) {
	isolate(t)
	f := newFakeEndpoint(t)
	answerNewCluster(f)
	t.Setenv("AWS_ENDPOINT_URL", f.server.URL)
	t.Setenv("AWS_REGION", exampleRegion)

	config := testClusterConfig()
	config.Region = ""
	_, err := NewCluster(context.Background(), config)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(f.last("CreateCluster").region, exampleRegion))
}

// TestClientNeedsARegion: with no Region anywhere, NewCluster refuses before any call.
func TestClientNeedsARegion(t *testing.T) {
	isolate(t)
	f := newFakeEndpoint(t)
	answerNewCluster(f)
	t.Setenv("AWS_ENDPOINT_URL", f.server.URL)

	config := testClusterConfig()
	config.Region = ""
	_, err := NewCluster(context.Background(), config)
	assert.ErrorContains(t, err, "no AWS Region is set")
	assert.Check(t, is.Len(f.targets(), 0))
}

// TestServiceSpecificEndpoints: AWS_ENDPOINT_URL_ECS and AWS_ENDPOINT_URL_CLOUDWATCH_LOGS each send their service to
// their own endpoint, and container logs are read from the CloudWatch Logs one.
func TestServiceSpecificEndpoints(t *testing.T) {
	isolate(t)
	ecsEndpoint := newFakeEndpoint(t)
	answerNewCluster(ecsEndpoint)
	logsEndpoint := newFakeEndpoint(t)
	logsEndpoint.answer("DescribeLogStreams", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"logStreams": []interface{}{
			map[string]interface{}{"logStreamName": body["logStreamNamePrefix"].(string) + "/web/0123456789abcdef0123456789abcdef"}}}
	})
	logsEndpoint.answer("GetLogEvents", func(body map[string]interface{}) (int, interface{}) {
		if body["nextToken"] != nil {
			return 200, map[string]interface{}{"events": []interface{}{}, "nextForwardToken": body["nextToken"]}
		}
		return 200, map[string]interface{}{"events": []interface{}{
			map[string]interface{}{"timestamp": 1, "message": "first line"},
			map[string]interface{}{"timestamp": 2, "message": "second line"},
		}, "nextForwardToken": "f/1"}
	})
	t.Setenv("AWS_ENDPOINT_URL_ECS", ecsEndpoint.server.URL)
	t.Setenv("AWS_ENDPOINT_URL_CLOUDWATCH_LOGS", logsEndpoint.server.URL)

	config := testClusterConfig()
	config.CloudWatchLogGroupName = "/ecs/example"
	cluster, err := NewCluster(context.Background(), config)
	assert.NilError(t, err)

	reader, err := cluster.GetContainerLogs(context.Background(), "default", "p1", "web", api.ContainerLogOpts{Tail: 10})
	assert.NilError(t, err)
	logs, err := ioutil.ReadAll(reader)
	assert.NilError(t, err)

	assert.Check(t, is.Equal(string(logs), "first line\nsecond line\n"))
	assert.DeepEqual(t, logsEndpoint.targets(), []string{"DescribeLogStreams", "GetLogEvents", "GetLogEvents"})
	assert.Check(t, is.Equal(logsEndpoint.last("DescribeLogStreams").signer, "logs"))
	assert.Check(t, is.Equal(logsEndpoint.last("DescribeLogStreams").body["logGroupName"], "/ecs/example"))
	assert.Check(t, is.Equal(logsEndpoint.last("DescribeLogStreams").body["logStreamNamePrefix"], "vk-podspec_test_default_p1_web"))
	for _, target := range ecsEndpoint.targets() {
		assert.Check(t, !strings.Contains(target, "Log"), "a CloudWatch Logs call reached the ECS endpoint: %s", target)
	}
}

func examplePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default", UID: "8d1f6b51-0d2a-4b5e-9a35-6f2a1d0c7e11"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:    "web",
			Image:   "public.ecr.aws/docker/library/busybox:latest",
			Command: []string{"sh", "-c"},
			Args:    []string{"sleep 3600"},
			Env:     []corev1.EnvVar{{Name: "GREETING", Value: "hello"}},
		}}},
	}
}

const exampleTaskArn = "arn:aws:ecs:" + exampleRegion + ":" + exampleAccount + ":task/test/0123456789abcdef0123456789abcdef"

// TestPodLifecycleRequests: the requests a pod's start, status and stop send, and how their answers are read.
func TestPodLifecycleRequests(t *testing.T) {
	isolate(t)
	f := newFakeEndpoint(t)
	answerNewCluster(f)
	family := "vk-podspec_test_default_p1"
	taskDefinitionArn := fmt.Sprintf("arn:aws:ecs:%s:%s:task-definition/%s:1", exampleRegion, exampleAccount, family)
	f.answer("RegisterTaskDefinition", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"taskDefinition": map[string]interface{}{
			"taskDefinitionArn": taskDefinitionArn, "family": body["family"], "revision": 1, "status": "ACTIVE"}}
	})
	f.answer("RunTask", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"failures": []interface{}{}, "tasks": []interface{}{map[string]interface{}{
			"taskArn": exampleTaskArn, "lastStatus": "PROVISIONING", "taskDefinitionArn": taskDefinitionArn}}}
	})
	f.answer("DescribeTasks", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"failures": []interface{}{}, "tasks": []interface{}{map[string]interface{}{
			"taskArn": exampleTaskArn, "lastStatus": "RUNNING", "createdAt": 1.7e9, "taskDefinitionArn": taskDefinitionArn,
			"attachments": []interface{}{map[string]interface{}{"type": "ElasticNetworkInterface", "status": "ATTACHED",
				"details": []interface{}{
					map[string]interface{}{"name": "subnetId", "value": exampleSubnet},
					map[string]interface{}{"name": "privateIPv4Address", "value": "10.0.0.10"},
				}}},
			"containers": []interface{}{map[string]interface{}{"name": "web", "lastStatus": "RUNNING"}},
		}}}
	})
	f.answer("StopTask", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"task": map[string]interface{}{"taskArn": exampleTaskArn, "desiredStatus": "STOPPED"}}
	})
	f.answer("DeregisterTaskDefinition", func(body map[string]interface{}) (int, interface{}) {
		return 200, map[string]interface{}{"taskDefinition": map[string]interface{}{"taskDefinitionArn": taskDefinitionArn, "status": "INACTIVE"}}
	})
	t.Setenv("AWS_ENDPOINT_URL", f.server.URL)
	ctx := context.Background()

	cluster, err := NewCluster(ctx, testClusterConfig())
	assert.NilError(t, err)
	pod, err := NewPod(ctx, cluster, examplePod())
	assert.NilError(t, err)

	registered := f.last("RegisterTaskDefinition").body
	assert.Check(t, is.Equal(registered["family"], family))
	assert.Check(t, is.Equal(registered["networkMode"], "awsvpc"))
	assert.Check(t, is.DeepEqual(registered["requiresCompatibilities"], []interface{}{"FARGATE"}))
	assert.Check(t, is.Equal(registered["cpu"], "256"))
	assert.Check(t, is.Equal(registered["memory"], "512"))
	containers := registered["containerDefinitions"].([]interface{})
	assert.Assert(t, is.Len(containers, 1))
	web := containers[0].(map[string]interface{})
	assert.Check(t, is.Equal(web["name"], "web"))
	assert.Check(t, is.Equal(web["image"], "public.ecr.aws/docker/library/busybox:latest"))
	assert.Check(t, is.Equal(web["cpu"], float64(256)))
	assert.Check(t, is.Equal(web["memory"], float64(512)))
	assert.Check(t, is.Equal(web["memoryReservation"], float64(512)))
	assert.Check(t, is.DeepEqual(web["entryPoint"], []interface{}{"sh", "-c"}))
	assert.Check(t, is.DeepEqual(web["command"], []interface{}{"sleep 3600"}))
	assert.Check(t, is.DeepEqual(web["environment"], []interface{}{map[string]interface{}{"name": "GREETING", "value": "hello"}}))

	assert.NilError(t, pod.Start(ctx))
	run := f.last("RunTask").body
	assert.Check(t, is.Equal(run["cluster"], "test"))
	assert.Check(t, is.Equal(run["launchType"], "FARGATE"))
	assert.Check(t, is.Equal(run["count"], float64(1)))
	assert.Check(t, is.Equal(run["taskDefinition"], taskDefinitionArn))
	assert.Check(t, is.Equal(run["startedBy"], "8d1f6b51-0d2a-4b5e-9a35-6f2a1d0c7e11"))
	assert.Check(t, is.Equal(run["platformVersion"], "LATEST"))
	assert.Check(t, is.DeepEqual(run["networkConfiguration"], map[string]interface{}{"awsvpcConfiguration": map[string]interface{}{
		"subnets": []interface{}{exampleSubnet}, "securityGroups": []interface{}{exampleSecurityGroup}, "assignPublicIp": "DISABLED"}}))

	status := pod.GetStatus(ctx)
	assert.Check(t, is.Equal(status.Phase, corev1.PodRunning))
	assert.Check(t, is.Equal(status.PodIP, "10.0.0.10"))
	assert.Assert(t, is.Len(status.ContainerStatuses, 1))
	assert.Check(t, status.ContainerStatuses[0].State.Running != nil)

	assert.NilError(t, pod.Stop(ctx))
	stop := f.last("StopTask").body
	assert.Check(t, is.Equal(stop["task"], exampleTaskArn))
	assert.Check(t, is.Equal(stop["cluster"], "test"))
	assert.Check(t, is.Equal(f.last("DeregisterTaskDefinition").body["taskDefinition"], taskDefinitionArn))
	_, err = cluster.GetPod("default", "p1")
	assert.Check(t, err != nil, "the stopped pod is still in the cluster")
}

// TestErrorAnswersAreErrors: an error answer to each call is returned as an error, never read as an answer.
func TestErrorAnswersAreErrors(t *testing.T) {
	serverError := func(body map[string]interface{}) (int, interface{}) {
		return 500, map[string]string{"__type": "ServerException", "message": "The service is unavailable."}
	}
	ctx := context.Background()

	t.Run("DescribeClusters", func(t *testing.T) {
		isolate(t)
		f := newFakeEndpoint(t)
		answerNewCluster(f)
		f.answer("DescribeClusters", serverError)
		t.Setenv("AWS_ENDPOINT_URL", f.server.URL)

		_, err := NewCluster(ctx, testClusterConfig())
		assert.ErrorContains(t, err, "ServerException")
		assert.DeepEqual(t, f.targets(), []string{"DescribeClusters"})
	})

	t.Run("RunTask and DescribeTasks", func(t *testing.T) {
		isolate(t)
		f := newFakeEndpoint(t)
		answerNewCluster(f)
		f.answer("RegisterTaskDefinition", func(body map[string]interface{}) (int, interface{}) {
			return 200, map[string]interface{}{"taskDefinition": map[string]interface{}{"taskDefinitionArn": "arn:aws:ecs:" +
				exampleRegion + ":" + exampleAccount + ":task-definition/x:1"}}
		})
		f.answer("RunTask", serverError)
		f.answer("DescribeTasks", serverError)
		t.Setenv("AWS_ENDPOINT_URL", f.server.URL)

		cluster, err := NewCluster(ctx, testClusterConfig())
		assert.NilError(t, err)
		pod, err := NewPod(ctx, cluster, examplePod())
		assert.NilError(t, err)
		assert.ErrorContains(t, pod.Start(ctx), "ServerException")
		pod.taskArn = exampleTaskArn
		assert.Check(t, is.Equal(pod.GetStatus(ctx).Phase, corev1.PodUnknown))
	})

	t.Run("RunTask failures", func(t *testing.T) {
		isolate(t)
		f := newFakeEndpoint(t)
		answerNewCluster(f)
		f.answer("RegisterTaskDefinition", func(body map[string]interface{}) (int, interface{}) {
			return 200, map[string]interface{}{"taskDefinition": map[string]interface{}{"taskDefinitionArn": "arn:aws:ecs:" +
				exampleRegion + ":" + exampleAccount + ":task-definition/x:1"}}
		})
		f.answer("RunTask", func(body map[string]interface{}) (int, interface{}) {
			return 200, map[string]interface{}{"tasks": []interface{}{}, "failures": []interface{}{
				map[string]interface{}{"reason": "RESOURCE:MEMORY"}}}
		})
		t.Setenv("AWS_ENDPOINT_URL", f.server.URL)

		cluster, err := NewCluster(ctx, testClusterConfig())
		assert.NilError(t, err)
		pod, err := NewPod(ctx, cluster, examplePod())
		assert.NilError(t, err)
		assert.ErrorContains(t, pod.Start(ctx), "RESOURCE:MEMORY")
	})
}
