# gke-node-optimizer

[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=flat)](http://makeapullrequest.com) 

A CLI tool optimizes preemptive and on-demand nodes in a gke cluster to make the best use of preemptive nodes.

- Restart a long running preemptive node
- Drain the on-demand node with the fewest number of pods if running
- Sends a report of the node status and optimization results

Docker image is available on Docker Hub.

- [na-ga/gke-node-optimizer](https://hub.docker.com/r/naaga/gke-node-optimizer)

## Motivation

Using preemptive nodes can reduce the cost of running a GKE cluster.
However, [preemptive nodes have some limitations](https://cloud.google.com/compute/docs/instances/preemptible#limitations).
The Cli tool will restart long running preemptive node to minimize the impact of those limitations.
And drain the on-demand node with the fewest number of pods If the on-demand nodes that can be reduced is running.

## Settings

The CLI tool sets the following environment variables:

- `PROJECT_ID`: project's ID (Required)
- `CLUSTER_NAME`: cluster's name (Required)
- `CLUSTER_LOCATION`: cluster's location (Required)
- `USE_LOCAL_KUBE_CONFIG`: true if you intend to use local kube config (Optional, Default=false)
- `MINIMUM_PREEMPTIBLE_NODE_COUNT`: expected minimum number of preemptible nodes (Optional, Default=auto)
- `OPTIMIZE_PREEMPTIBLE_NODE`: true if you intend to optimize the preemptible node (Optional, Default=true)
- `OPTIMIZE_AUTOSCALE_ONDEMAND_NODE`: true if you intend to optimize the on-demand node (Optional, Default=true)
- `SLACK_BOT_TOKEN`: user token for slack bot if you intend to send report to slack (Optional, Default=empty)
- `SLACK_CHANNEL_ID`: channel ID for slack bot if you intend to send report to slack (Optional, Default=empty)

## Example

The example below assumes that the total number of resource requests for all pods can be satisfied in 18 nodes and that the total number of resource requests for pods that do not have fault-tolerance can be satisfied in 3 nodes.

### Cluster Settings

Create regional GKE clusters with three types of node pools.

1. `default-pool`: node pool that on-demand instances are always running with 1 node per zone
1. `preemptible-pool`: node pool that preemptible instances are running between 5 and 6 nodes per zone by autoscaler
1. `ondemand-pool`: node pool that on-demand instances are running between 0 and 6 nodes per zone by autoscaler

These node pools are used for the following purposes.

1. `default-pool`: used by non-fault-tolerant pods or important pods
1. `preemptive-pools`: used for fault-tolerant pods or not important pods
1. `ondemand-pool`: used when `preemptible-pool` is not available

To create regional GKE cluster and `default-pool` node pool with 1 node per zone, you can use the following command:

```shell script
$ gcloud config set project my-project
$ gcloud container clusters create my-cluster --region=asia-east1 --num-nodes=1 --enable-ip-alias
```

The `preemptible-pool` and `ondemand-pool` node pools require a maximum of 5 nodes per zone.
However, since the target nodes are temporarily unavailable during the optimization process, configure the maximum number of nodes to 6 nodes per zone. 
To create the `preemptible-pool` and `ondemand-pool` node pools, you can use the following command:

```shell script
$ gcloud container node-pools create preemptible-pool --cluster my-cluster --region=asia-east1 --enable-autoscaling --min-nodes=5 --max-nodes=6 --preemptible
$ gcloud container node-pools create ondemand-pool --cluster my-cluster --region=asia-east1 --enable-autoscaling --min-nodes=0 --max-nodes=6
```

Immediately after creating the node pool, excess nodes are started, but are automatically adjusted by the autoscaler.
Although the number of nodes may temporarily increase during the optimization process.
Normally, the 3 nodes managed by `default-pool` and 15 nodes managed by `preemptible-pool` are running.

### Priority Class Settings

Create the four priority classes.
To create a priority class, you can use the following command:

```shell script
$ kubectl apply -f ./example/priority-class.yaml
```

or

```shell script
$ cat << EOS | kubectl apply -f -
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: high-priority
value: 100
globalDefault: false
description: "This priority class should be used for high priority service pods only."

---

apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: middle-priority
value: 50
globalDefault: false
description: "This priority class should be used for middle priority service pods only."

---

apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: default-priority
value: 25
globalDefault: true
description: "This priority class will be used as the default value for all service pods."

---

apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: low-priority
value: 0
globalDefault: false
description: "This priority class should be used for low priority service pods only."
EOS
```

### Application Settings

Set pod disruption budget to avoid situations where multiple pods are not available at the same time.
To create a pod disruption budget, you can use the following command:

```shell script
$ cat << EOS | kubectl apply -f -
apiVersion: policy/v1beta1
kind: PodDisruptionBudget
metadata:
  name: my-pod
spec:
  maxUnavailable: 50%
  selector:
    matchLabels:
      name: my-pod
EOS
```

Set resource controls, priority classes, and node affinity so that pods are scheduled on the appropriate nodes.
First, set resource controls on all pod containers.
This is a very important setting because pod scheduling is performed with reference to resource requests.
The following is an example of deployment settings.

```yaml
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: my-pod
          resources:
            limits:
              cpu: "50m"
              memory: "128Mi"
            requests:
              cpu: "50m"
              memory: "128Mi"
```

Next, configure priority class and node affinity so that fault-tolerant pods that can be force shutdown, or not important pods, are scheduled in the `preemptive-pool`.
The following is a sample when setting to Deployment.

```yaml
kind: Deployment
spec:
  template:
    spec:
      priorityClassName: low-priority # if not important pods
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              preference:
                matchExpressions:
                  - key: cloud.google.com/gke-nodepool
                    operator: In
                    values:
                      - preemptible-pool
```

Next, configure priority class and node affinity so that fault-tolerant pods that require a graceful shutdown are scheduled in the `default-pool` or `ondemand-pool`.
To reduce the number of on-demand nodes, avoid this setting as much as possible.
The following is an example of the Deployment settings.

```yaml
kind: Deployment
spec:
  template:
    spec:
      priorityClassName: middle-priority
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: cloud.google.com/gke-nodepool
                    operator: In
                    values:
                      - preemptible-pool
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              preference:
                matchExpressions:
                  - key: cloud.google.com/gke-nodepool
                    operator: In
                    values:
                      - default-pool
```

Finally, configure priority class and node affinity so that non-fault-tolerant pods, or important pods, are scheduled in the `default-pool`.
To reduce the number of on-demand nodes, avoid this setting as much as possible.
The following is an example of the Deployment settings.

```yaml
kind: Deployment
spec:
  template:
    spec:
      priorityClassName: high-priority
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                - key: cloud.google.com/gke-nodepool
                  operator: In
                  values:
                    - default-pool
```

### Optimizer Settings

Run this Cli tool using CronJob.
The example configuration uses up to 18 preemptive nodes, so you can reset the 24-hour counter in advance by running at 18/24 hour intervals.
To create a CronJob that runs every hour, you can use the following command:

```yaml
$ cat << EOS > | kubectl apply -f -
apiVersion: batch/v1beta1
kind: CronJob
metadata:
  name: gke-node-optimizer
  labels:
    name: gke-node-optimizer
spec:
  schedule: "0 * * * *" # every 1 hour
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 0
  failedJobsHistoryLimit: 1
  jobTemplate:
    spec:
      backoffLimit: 0 # No retry when failed
      template:
        metadata:
          labels:
            name: gke-node-optimizer
        spec:
          priorityClassName: high-priority
          affinity:
            nodeAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                nodeSelectorTerms:
                  - matchExpressions:
                    - key: cloud.google.com/gke-nodepool
                      operator: In
                      values:
                        - default-pool
          restartPolicy: Never
          containers:
          - name: gke-node-optimizer
            image: naaga/gke-node-optimizer:v0.1.0
            imagePullPolicy: IfNotPresent # Pulled only if not already present locally
            env:
            - name: PROJECT_ID
              value: "my-project"
            - name: CLUSTER_NAME
              value: "my-cluster"
            - name: CLUSTER_LOCATION
              value: "asia-east1"
            - name: SLACK_BOT_TOKEN
              value: "TODO" # Replace with the slack bot user token
            - name: SLACK_CHANNEL_ID
              value: "TODO" # Replace with the slack channelId of the report destination
EOS
```

### Execute Optimizer

To execute this Cli immediately using CronJob settings, you can use the following command:

```shell script
$ gcloud config set project my-project
$ gcloud container clusters get-credentials my-cluster --region=asia-east1
$ make run-gke 
```

### Signal Handling

The CLI tool avoided a force shutdown by restarting within 24 hours.
However, preemptible nodes may be force shutdown for reasons other than the 24-hour counter.
Compute Engine sends a preemption notice to the instance in the form of an ACPI G2 Soft Off signal.

You can use a shutdown script to handle the preemption notice and pass the signal to the pods before the instance stops.
If you are using COS, you can use a startup script to set kubectl and credentials, and then use a shutdown script to drain node.
Although omitted in this example, it is necessary to change the instance template and apply it to the node pool.
