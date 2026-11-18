module github.com/virtual-kubelet/aws-fargate

go 1.24

require (
	contrib.go.opencensus.io/exporter/ocagent v0.5.0
	github.com/BurntSushi/toml v0.3.1
	github.com/aws/aws-sdk-go-v2 v1.48.0
	github.com/aws/aws-sdk-go-v2/config v1.33.19
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.92.2
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.349.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.106.0
	github.com/aws/aws-sdk-go-v2/service/iam v1.66.0
	github.com/mitchellh/go-homedir v1.1.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.2.0
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.3
	github.com/virtual-kubelet/virtual-kubelet v0.10.0
	go.opencensus.io v0.21.0
	gotest.tools v2.2.0+incompatible
	k8s.io/api v0.0.0-20190615205754-1d1b8b084b30
	k8s.io/apimachinery v0.0.0-20190612125636-6a5db36e93ad
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/klog v0.3.1
)

require (
	git.apache.org/thrift.git v0.12.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.27 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.21.1 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.9 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.25 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.13.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.39.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.44.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.52.0 // indirect
	github.com/aws/smithy-go v1.28.9 // indirect
	github.com/census-instrumentation/opencensus-proto v0.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/elazarl/goproxy v0.0.0-20190421051319-9d40249d3c2f // indirect
	github.com/gogo/protobuf v1.1.1 // indirect
	github.com/golang/groupcache v0.0.0-20190129154638-5b532d6fd5ef // indirect
	github.com/golang/protobuf v1.3.1 // indirect
	github.com/google/go-cmp v0.2.0 // indirect
	github.com/google/gofuzz v0.0.0-20170612174753-24818f796faf // indirect
	github.com/googleapis/gnostic v0.0.0-20170729233727-0c5108395e2d // indirect
	github.com/gorilla/mux v1.7.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.8.5 // indirect
	github.com/hashicorp/golang-lru v0.5.0 // indirect
	github.com/imdario/mergo v0.3.7 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/json-iterator/go v1.1.6 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.1 // indirect
	github.com/onsi/ginkgo v1.8.0 // indirect
	github.com/onsi/gomega v1.5.0 // indirect
	golang.org/x/crypto v0.0.0-20190617133340-57b3e21c3d56 // indirect
	golang.org/x/net v0.0.0-20190613194153-d28f0bde5980 // indirect
	golang.org/x/oauth2 v0.0.0-20190402181905-9f3314589c9a // indirect
	golang.org/x/sync v0.0.0-20190423024810-112230192c58 // indirect
	golang.org/x/sys v0.0.0-20190616124812-15dcb6c0061f // indirect
	golang.org/x/text v0.3.0 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4 // indirect
	google.golang.org/api v0.4.0 // indirect
	google.golang.org/appengine v1.5.0 // indirect
	google.golang.org/genproto v0.0.0-20190307195333-5fe7a883aa19 // indirect
	google.golang.org/grpc v1.20.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.2.2 // indirect
	k8s.io/apiserver v0.0.0-20190615170205-3722cb685593 // indirect
	k8s.io/kube-openapi v0.0.0-20190228160746-b3a7cee44a30 // indirect
	k8s.io/kubernetes v1.14.3 // indirect
	k8s.io/utils v0.0.0-20190607212802-c55fbcfc754a // indirect
	sigs.k8s.io/yaml v1.1.0 // indirect
)

replace k8s.io/api => k8s.io/api v0.0.0-20190606204050-af9c91bd2759

replace k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190404173353-6a84e37a896d

replace k8s.io/client-go => k8s.io/client-go v11.0.1-0.20190606204521-b8faab9c5193+incompatible

replace k8s.io/kubernetes => k8s.io/kubernetes v1.14.3

replace go.opencensus.io => go.opencensus.io v0.19.3
