package gke

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/na-ga/gke-node-optimizer/log"

	containerV1 "cloud.google.com/go/container/apiv1"
	"golang.org/x/oauth2/google"
	computeV1 "google.golang.org/api/compute/v1"
	containerProtoV1 "google.golang.org/genproto/googleapis/container/v1"
	coreV1 "k8s.io/api/core/v1"
	policyV1beta1 "k8s.io/api/policy/v1beta1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	// execute init for kubeconfig auth via gcloud
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

const (
	NodePoolLabel        = "cloud.google.com/gke-nodepool"
	PreemptibleLabel     = "cloud.google.com/gke-preemptible"
	NodeRegionLabel      = "failure-domain.beta.kubernetes.io/region"
	NodeZoneLabel        = "failure-domain.beta.kubernetes.io/zone"
	ResourceEvictionKind = "Eviction"
	ResourceEvictionName = "pods/eviction"
)

//
type Client interface {
	// GetCluster returns the owned cluster.
	GetCluster(ctx context.Context) (*Cluster, error)
	// GetNodePool returns the node pool by the node pool name.
	GetNodePool(ctx context.Context, nodePoolName string) (*NodePool, error)
	// GetNodePoolList returns the node pool list by the owned cluster.
	GetNodePoolList(ctx context.Context) ([]*NodePool, error)
	// GetNode returns the node by the node name.
	GetNode(nodeName string) (*Node, error)
	// GetNodeList returns the nodes into the owned cluster.
	GetNodeList() ([]*Node, error)
	// GetPod returns the pod by the pod name.
	GetPod(podName string) (*Pod, error)
	// GetPodListByNodeName returns the pod list by the node name.
	GetPodListByNodeName(nodeName string) ([]*Pod, error)
	// RefreshNode drains node and deletes node if preemptible.
	RefreshNode(ctx context.Context, nodeName string) (evictedPods []*Pod, err error)
	// RefreshNodes drains nodes and deletes nodes if preemptible.
	RefreshNodes(ctx context.Context, nodeNames []string) (evictedPods []*Pod, err error)
}

//
type client struct {
	project          string
	clusterName      string
	clusterLocation  string
	clusterManager   *containerV1.ClusterManagerClient
	kubernetesClient *kubernetes.Clientset
	computeClient    *computeV1.Service
}

//
type Cluster struct {
	Name        string
	ResourceURL string
	Region      string
	Status      containerProtoV1.Cluster_Status
	NodePool    []*NodePool
}

//
type NodePool struct {
	Name              string
	ResourceURL       string
	Preemptible       bool
	Autoscale         bool
	MinNodeCount      int
	MaxNodeCount      int
	Status            containerProtoV1.NodePool_Status
	InstanceGroupURLs []string
}

//
type Node struct {
	Name        string
	ResourceURL string
	ClusterName string
	NodePool    string
	Region      string
	Zone        string
	Ready       bool
	Preemptible bool
	Age         time.Duration
	Pods        []*Pod
}

//
type Pod struct {
	Name      string
	Namespace string
	NodeName  string
	Hostname  string
	Status    coreV1.PodStatus
}

//
func New(ctx context.Context, project, clusterName, clusterLocation string, useLocalConfig bool) (Client, error) {
	cli, err := google.DefaultClient(ctx, computeV1.ComputeScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create google default client: %s", err)
	}
	computeClient, err := computeV1.New(cli)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %s", err)
	}
	clusterManager, err := containerV1.NewClusterManagerClient(ctx)
	if err != nil {
		return nil, err
	}
	var kubernetesConfig *rest.Config
	if useLocalConfig {
		kubernetesConfig, err = clientcmd.BuildConfigFromFlags("", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
	} else {
		kubernetesConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config: %s", err)
	}
	kubernetesClient, err := kubernetes.NewForConfig(kubernetesConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %s", err)
	}
	ret := &client{
		project:          project,
		clusterName:      clusterName,
		clusterLocation:  clusterLocation,
		computeClient:    computeClient,
		kubernetesClient: kubernetesClient,
		clusterManager:   clusterManager,
	}
	return ret, nil
}

//
func (cli *client) GetCluster(ctx context.Context) (*Cluster, error) {
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", cli.project, cli.clusterLocation, cli.clusterName)
	req := &containerProtoV1.GetClusterRequest{Name: name}
	res, err := cli.clusterManager.GetCluster(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster %s: %s", name, err)
	}
	ret := &Cluster{
		Name:        res.Name,
		Region:      res.Location,
		ResourceURL: fmt.Sprintf("https://console.cloud.google.com/kubernetes/clusters/details/%s/%s?project=%s", res.Location, res.Name, cli.project),
		Status:      res.Status,
		NodePool:    cli.toNodePools(res.NodePools),
	}
	return ret, nil
}

//
func (cli *client) GetNodePool(ctx context.Context, nodePoolName string) (*NodePool, error) {
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", cli.project, cli.clusterLocation, cli.clusterName, nodePoolName)
	req := &containerProtoV1.GetNodePoolRequest{Name: name}
	res, err := cli.clusterManager.GetNodePool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get node pool %s: %s", name, err)
	}
	return cli.toNodePool(res), nil
}

//
func (cli *client) GetNodePoolList(ctx context.Context) ([]*NodePool, error) {
	parent := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", cli.project, cli.clusterLocation, cli.clusterName)
	req := &containerProtoV1.ListNodePoolsRequest{Parent: parent}
	res, err := cli.clusterManager.ListNodePools(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get node pool list %s: %s", parent, err)
	}
	return cli.toNodePools(res.NodePools), nil
}

//
func (cli *client) GetNode(nodeName string) (*Node, error) {
	res, err := cli.kubernetesClient.CoreV1().Nodes().Get(nodeName, metaV1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node list: %s", err)
	}
	node := cli.toNode(*res)
	pods, err := cli.GetPodListByNodeName(node.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get pods on node %s: %s", node.Name, err)
	}
	node.Pods = pods
	return node, nil
}

//
func (cli *client) GetNodeList() ([]*Node, error) {
	nl, err := cli.kubernetesClient.CoreV1().Nodes().List(metaV1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node list: %s", err)
	}
	nodes := cli.toNodes(nl.Items)
	for _, v := range nodes {
		pods, err := cli.GetPodListByNodeName(v.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get pods on node %s: %s", v.Name, err)
		}
		v.Pods = pods
	}
	return nodes, nil
}

//
func (cli *client) GetPod(podName string) (*Pod, error) {
	pods, err := cli.kubernetesClient.CoreV1().Pods(metaV1.NamespaceAll).List(metaV1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"name": podName}).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("falid to get pod by pod name %s: %s", podName, err)
	}
	if len(pods.Items) != 1 {
		return nil, fmt.Errorf("unexpected reuslt length: expect=1, actual=%d", len(pods.Items))
	}
	return cli.toPod(pods.Items[0]), nil
}

//
func (cli *client) GetPodListByNodeName(nodeName string) ([]*Pod, error) {
	pods, err := cli.kubernetesClient.CoreV1().Pods(metaV1.NamespaceAll).List(metaV1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("falid to get pod list by node name %s: %s", nodeName, err)
	}
	return cli.toPods(pods.Items), nil
}

//
func (cli *client) RefreshNode(ctx context.Context, nodeName string) (evictedPods []*Pod, err error) {
	node, err := cli.GetNode(nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get node %s: %s", nodeName, err)
	}
	if err := cli.cordonNode(node.Name); err != nil {
		return nil, fmt.Errorf("failed to cordon node %s: %s", node.Name, err)
	}
	cordonNode := node
	defer func() {
		if cordonNode != nil {
			if e := cli.uncordonNode(cordonNode.Name); e != nil {
				if err == nil {
					err = fmt.Errorf("failed to uncordon node %s: %s", cordonNode.Name, e)
				} else {
					err = fmt.Errorf("failed to uncordon node %s: %s: %s", cordonNode.Name, e, err)
				}
			}
		}
	}()
	evictedPods, err = cli.drainNode(node)
	if err != nil {
		return nil, fmt.Errorf("failed to drain node %s: %s", node.Name, err)
	}
	if node.Preemptible {
		cordonNode = nil // reset
		if err := cli.deleteNode(ctx, node); err != nil {
			return evictedPods, fmt.Errorf("failed to delete node %s: %s", node.Name, err)
		}
		log.Infof("Succeeded in stop instance: %s", node.Name)
	}
	return evictedPods, nil
}

//
func (cli *client) RefreshNodes(ctx context.Context, nodeNames []string) (evictedPods []*Pod, err error) {
	nodes := make([]*Node, 0, len(nodeNames))
	for _, v := range nodeNames {
		node, err := cli.GetNode(v)
		if err != nil {
			return nil, fmt.Errorf("failed to get node %s: %s", v, err)
		}
		nodes = append(nodes, node)
	}
	cordonNodes := make(map[string]*Node, len(nodes))
	defer func() {
		for _, node := range cordonNodes {
			if e := cli.uncordonNode(node.Name); e != nil {
				if err == nil {
					err = fmt.Errorf("failed to uncordon node %s: %s", node.Name, e)
				} else {
					err = fmt.Errorf("failed to uncordon node %s: %s: %s", node.Name, e, err)
				}
			}
		}
	}()
	for _, node := range nodes {
		if err := cli.cordonNode(node.Name); err != nil {
			return nil, fmt.Errorf("failed to cordon node %s: %s", node.Name, err)
		}
		cordonNodes[node.Name] = node
	}
	evictedPods = make([]*Pod, 0, len(nodes)*32) // maximum pods per node default value is 32
	for i, node := range nodes {
		if i > 0 {
			log.Infof("Waiting 1 minute for evicted pods on %s to running.", nodes[i-1].Name)
			time.Sleep(time.Minute)
		}
		pods, err := cli.drainNode(node)
		evictedPods = append(evictedPods, pods...)
		if err != nil {
			return evictedPods, fmt.Errorf("failed to drain node %s: %s", node.Name, err)
		}
		if node.Preemptible {
			if err := cli.deleteNode(ctx, node); err != nil {
				return evictedPods, fmt.Errorf("failed to delete node %s: %s", node.Name, err)
			}
			delete(cordonNodes, node.Name) // reset
			if _, err := cli.computeClient.Instances.Stop(cli.project, node.Zone, node.Name).Context(ctx).Do(); err != nil {
				return evictedPods, fmt.Errorf("failed to stop instance %s: %s", node.Name, err)
			}
			log.Infof("Succeeded in stop instance: %s", node.Name)
		}
	}
	return evictedPods, nil
}

//
func (cli *client) toNodePools(in []*containerProtoV1.NodePool) []*NodePool {
	out := make([]*NodePool, 0, len(in))
	for _, v := range in {
		out = append(out, cli.toNodePool(v))
	}
	return out
}

//
func (cli *client) toNodePool(in *containerProtoV1.NodePool) *NodePool {
	autoscale := in.Autoscaling != nil && in.Autoscaling.Enabled
	var minNodeCount, maxNodeCount int
	if autoscale {
		minNodeCount = int(in.Autoscaling.MinNodeCount)
		maxNodeCount = int(in.Autoscaling.MaxNodeCount)
	}
	return &NodePool{
		Name:              in.Name,
		ResourceURL:       fmt.Sprintf("https://console.cloud.google.com/kubernetes/nodepool/%s/%s/%s?project=%s", cli.clusterLocation, cli.clusterName, in.Name, cli.project),
		Autoscale:         autoscale,
		MinNodeCount:      minNodeCount,
		MaxNodeCount:      maxNodeCount,
		InstanceGroupURLs: in.InstanceGroupUrls,
		Preemptible:       in.Config.Preemptible,
		Status:            in.Status,
	}
}

//
func (cli *client) toNodes(in []coreV1.Node) []*Node {
	out := make([]*Node, 0, len(in))
	for _, v := range in {
		if node := cli.toNode(v); node != nil {
			out = append(out, node)
		}
	}
	return out
}

//
func (cli *client) toNode(in coreV1.Node) *Node {
	labels := in.Labels
	pool, ok := labels[NodePoolLabel]
	if !ok {
		log.Errorf("Ignore node because label %s is not exists: name=%s", NodePoolLabel, in.Name)
		return nil
	}
	nodeNamePrefix := "gke-" + cli.clusterName + "-" + pool
	if cli.clusterName != "" && !strings.HasPrefix(in.Name, nodeNamePrefix) { // n.ClusterName is empty string
		log.Errorf("Ignore node because unexpected node name prefix: name=%s, prefix=%s", in.Name, nodeNamePrefix)
		return nil
	}
	region, ok := labels[NodeRegionLabel]
	if !ok {
		log.Errorf("Ignore node because label %s is not exists: name=%s", NodeRegionLabel, in.Name)
		return nil
	}
	zone, ok := labels[NodeZoneLabel]
	if !ok {
		log.Errorf("Ignore node because label %s is not exists: name=%s", NodeZoneLabel, in.Name)
		return nil
	}
	ready := false
	condNum := len(in.Status.Conditions)
	if condNum > 0 && in.Status.Conditions[condNum-1].Type == coreV1.NodeReady {
		if !in.Spec.Unschedulable {
			ready = true // unschedulable flag is enable while draining
		}
	}
	return &Node{
		ClusterName: cli.clusterName, // not use `n.ClusterName` because always empty string
		Name:        in.Name,
		ResourceURL: fmt.Sprintf("https://console.cloud.google.com/kubernetes/node/%s/%s/%s?project=%s", region, cli.clusterName, in.Name, cli.project),
		NodePool:    pool,
		Region:      region,
		Zone:        zone,
		Age:         time.Now().Sub(in.CreationTimestamp.Time),
		Ready:       ready,
		Preemptible: labels[PreemptibleLabel] == "true",
	}
}

//
func (cli *client) toPods(in []coreV1.Pod) []*Pod {
	out := make([]*Pod, 0, len(in))
	for _, v := range in {
		out = append(out, cli.toPod(v))
	}
	return out
}

//
func (cli *client) toPod(in coreV1.Pod) *Pod {
	return &Pod{
		Name:      in.Name,
		Namespace: in.Namespace,
		NodeName:  in.Spec.NodeName,
		Hostname:  in.Spec.Hostname,
		Status:    in.Status,
	}
}

//
func (cli *client) drainNode(node *Node) ([]*Pod, error) {
	policy, err := cli.policyVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get policy version of node %s: %s", node.Name, err)
	}
	return cli.evictPods(node, policy)
}

//
func (cli *client) policyVersion() (string, error) {
	discoveryClient := cli.kubernetesClient.Discovery()
	groupList, err := discoveryClient.ServerGroups()
	if err != nil {
		return "", fmt.Errorf("failed to get server groups: %s", err)
	}
	foundPolicyGroup := false
	var policyGroupVersion string
	for _, group := range groupList.Groups {
		if group.Name == "policy" {
			foundPolicyGroup = true
			policyGroupVersion = group.PreferredVersion.GroupVersion
			break
		}
	}
	if !foundPolicyGroup {
		return "", nil
	}
	resourceList, err := discoveryClient.ServerResourcesForGroupVersion("v1")
	if err != nil {
		fmt.Errorf("failed to get server resource for group verison v1: %s", err)
		return "", err
	}
	for _, resource := range resourceList.APIResources {
		if resource.Name == ResourceEvictionName && resource.Kind == ResourceEvictionKind {
			return policyGroupVersion, nil
		}
	}
	return "", nil
}

//
func (cli *client) cordonNode(nodeName string) error {
	return cli.applyCordonOrUncordon(nodeName, true)
}

//
func (cli *client) uncordonNode(nodeName string) error {
	return cli.applyCordonOrUncordon(nodeName, false)
}

//
func (cli *client) applyCordonOrUncordon(nodeName string, cordon bool) error {
	status := "cordon"
	if !cordon {
		status = "uncordon"
	}
	n, err := cli.kubernetesClient.CoreV1().Nodes().Get(nodeName, metaV1.GetOptions{})
	if err != nil {
		return err
	}
	if n.Spec.Unschedulable == cordon {
		log.Infof("Already %s: %s\n", status, nodeName)
		return nil // returns not error
	}
	n.Spec.Unschedulable = cordon
	if _, err = cli.kubernetesClient.CoreV1().Nodes().Update(n); err != nil {
		return err
	}
	log.Infof("Succeeded in %s node: %s", status, nodeName)
	return err
}

//
func (cli *client) evictPods(node *Node, policy string) ([]*Pod, error) {
	evicted := make([]*Pod, 0, len(node.Pods))
	for _, pod := range node.Pods {
		eviction := &policyV1beta1.Eviction{
			TypeMeta: metaV1.TypeMeta{
				APIVersion: policy,
				Kind:       ResourceEvictionKind,
			},
			ObjectMeta: metaV1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		for i := 1; true; i++ {
			err := cli.kubernetesClient.PolicyV1beta1().Evictions(eviction.Namespace).Evict(eviction)
			if err == nil {
				break
			}
			if err.Error() != "Cannot evict pod as it would violate the pod's disruption budget." {
				return evicted, fmt.Errorf("failed to evict pod %s. count=%d: %s", pod.Name, i, err)
			}
			if i > 2 {
				return evicted, fmt.Errorf("failed to evict pod %s because give up. count=%d: %s", pod.Name, i, err)
			}
			log.Warnf("Waiting 30 seconds for evicted pod to running %s. count=%d: %s", pod.Name, i, err)
			time.Sleep(time.Second * 30)
		}
		evicted = append(evicted, pod)
		log.Infof("Succeeded in evicted pod %s on node %s", pod.Name, pod.NodeName)
	}
	return evicted, nil
}

//
func (cli *client) deleteNode(ctx context.Context, node *Node) error {
	n, err := cli.kubernetesClient.CoreV1().Nodes().Get(node.Name, metaV1.GetOptions{})
	if err != nil {
		return fmt.Errorf("falid to get node %s: %s", node.Name, err)
	}
	if !n.Spec.Unschedulable {
		return fmt.Errorf("detect schedulable flag, aborting deleteNode node %s: %s", node.Name, err)
	}
	if err := cli.kubernetesClient.CoreV1().Nodes().Delete(node.Name, &metaV1.DeleteOptions{}); err != nil {
		return fmt.Errorf("detect schedulable flag, aborting deleteNode node %s: %s", node.Name, err)
	}
	if _, err := cli.computeClient.Instances.Stop(cli.project, node.Zone, node.Name).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to stop instance %s: %s", node.Name, err)
	}
	log.Infof("Succeeded in delete node: %s", node.Name)
	return nil
}
