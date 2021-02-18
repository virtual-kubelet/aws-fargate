
Run the following to deploy the chart with rquired settings (Replace the values)

```bash
helm install --name virtual-kubelet \
--namespace vk-aws \
--set aws.region=us-east-1 \
--set aws.clusterName=ecs-cluster \
--set aws.subnets[0]="subnet-05d3164d0ab1c734f" \
--set aws.subnets[1]="subnet-054d7d5e3941b99b3" \
--set aws.securityGroups[0]="sg-02ea90a0fb77f5cd4" \
--set nodename=vk-node2
```

