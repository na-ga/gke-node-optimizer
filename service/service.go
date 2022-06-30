package service

import (
	"context"
	"fmt"

	"github.com/na-ga/gke-node-optimizer/gke"
	"github.com/na-ga/gke-node-optimizer/log"
	"github.com/na-ga/gke-node-optimizer/report"

	"google.golang.org/genproto/googleapis/container/v1"
)

type (
	//
	Optimizer struct {
		client gke.Client
		result *report.Result
		option OptimizerOption
	}

	//
	OptimizerOption struct {
		MinimumPreemptibleNodeCount   int
		OptimizePreemptibleNode       bool
		OptimizeAutoscaleOndemandNode bool
	}
)

//
func NewOptimizer(client gke.Client, result *report.Result, option OptimizerOption) *Optimizer {
	return &Optimizer{
		client: client,
		result: result,
		option: option,
	}
}

//
func (o *Optimizer) Optimize(ctx context.Context) error {

	// Check cluster
	cluster, err := o.client.GetCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %s", err.Error())
	}
	o.result.Cluster = cluster
	if cluster.Status != container.Cluster_RUNNING {
		return fmt.Errorf("cluster status is not running: %s", cluster.Status.String())
	}

	// Check node pools
	preemptibleNodePools := make([]*gke.NodePool, 0, len(cluster.NodePool))
	ondemandAutoscaleNodePools := make([]*gke.NodePool, 0, len(cluster.NodePool))
	for _, v := range cluster.NodePool {
		if v.Status != container.NodePool_RUNNING {
			return fmt.Errorf("detected not running node pool: name=%s, status=%s", v.Name, v.Status)
		}
		if v.Preemptible {
			preemptibleNodePools = append(preemptibleNodePools, v)
		} else if v.Autoscale {
			ondemandAutoscaleNodePools = append(ondemandAutoscaleNodePools, v)
		}
		log.Infof("Fetch node-pool. name=%s, preemptible=%t, autoscale=%t", v.Name, v.Preemptible, v.Autoscale)
	}
	if len(preemptibleNodePools) == 0 {
		return fmt.Errorf("preemptible node pools is not exists")
	}

	// Check nodes
	nodes, err := o.client.GetNodeList(ctx)
	if err != nil {
		return fmt.Errorf("failed to get node list: %s", err.Error())
	}
	if len(nodes) == 0 {
		return fmt.Errorf("node does not exist")
	}
	o.result.ActiveNodes = nodes
	nodesByPool := make(map[string][]*gke.Node, len(cluster.NodePool))
	for _, v := range nodes {
		if !v.Ready {
			return fmt.Errorf("detected not ready node: name=%s", v.Name)
		}
		if _, ok := nodesByPool[v.NodePool]; !ok {
			nodesByPool[v.NodePool] = make([]*gke.Node, 0, len(nodes))
		}
		nodesByPool[v.NodePool] = append(nodesByPool[v.NodePool], v)
		log.Infof("Fetch node. name=%s, preemptible=%t, age=%s, pods=%d", v.Name, v.Preemptible, v.Age.String(), len(v.Pods))
	}
	o.result.ActiveNodePools = make([]*gke.NodePool, 0, len(cluster.NodePool))
	for _, v := range cluster.NodePool {
		if _, ok := nodesByPool[v.Name]; ok {
			o.result.ActiveNodePools = append(o.result.ActiveNodePools, v)
		}
	}

	// Check preemptible node
	minPreemptibleNodeCount := 0
	preemptibleNodes := make([]*gke.Node, 0, len(nodes))
	for _, v := range preemptibleNodePools {
		minPreemptibleNodeCount += v.MinNodeCount * len(v.InstanceGroupURLs)
		if len(nodesByPool[v.Name]) > 0 {
			preemptibleNodes = append(preemptibleNodes, nodesByPool[v.Name]...)
		}
	}
	if minPreemptibleNodeCount < o.option.MinimumPreemptibleNodeCount {
		minPreemptibleNodeCount = o.option.MinimumPreemptibleNodeCount
	}
	o.result.PreemptibleNodeActualCount = len(preemptibleNodes)
	o.result.PreemptibleNodeMinimumCount = minPreemptibleNodeCount
	if len(preemptibleNodes) < minPreemptibleNodeCount {
		return fmt.Errorf("the minimum number of preemptible nodes condition is not satisfied: expect=%d, actual=%d", minPreemptibleNodeCount, len(preemptibleNodes))
	}

	// Check ondemand auto scale nodes
	ondemandAutoscaleNodes := make([]*gke.Node, 0, len(nodes))
	for _, v := range ondemandAutoscaleNodePools {
		if v.Autoscale && len(nodesByPool[v.Name]) > 0 {
			ondemandAutoscaleNodes = append(ondemandAutoscaleNodes, nodesByPool[v.Name]...)
		}
	}

	// Select target preemptible node
	targetNodeNames := make([]string, 0, 2)
	var oldestPreemptibleNode *gke.Node
	for _, node := range preemptibleNodes {
		if oldestPreemptibleNode == nil || oldestPreemptibleNode.Age < node.Age {
			oldestPreemptibleNode = node // Choose the node with the longest uptime
		}
	}
	if oldestPreemptibleNode != nil {
		log.Infof("Refresh oldest preemptive node: name=%s, nodePoolName=%s, age=%s", oldestPreemptibleNode.Name, oldestPreemptibleNode.NodePool, oldestPreemptibleNode.Age)
		o.result.TargetPreemptibleNode = oldestPreemptibleNode
		if o.option.OptimizePreemptibleNode {
			targetNodeNames = append(targetNodeNames, oldestPreemptibleNode.Name)
		}
	}

	// Check target ondemand auto scale node
	var targetOndemandAutoscaleNode *gke.Node
	for _, node := range ondemandAutoscaleNodes {
		if targetOndemandAutoscaleNode == nil || len(targetOndemandAutoscaleNode.Pods) > len(node.Pods) {
			targetOndemandAutoscaleNode = node // Choose the node with the fewest pods
		}
	}
	if targetOndemandAutoscaleNode != nil {
		log.Infof("Refresh target ondemand auto scale node: name=%s, nodePoolName=%s, age=%s", targetOndemandAutoscaleNode.Name, targetOndemandAutoscaleNode.NodePool, targetOndemandAutoscaleNode.Age)
		o.result.TargetOndemandAutoscaleNode = targetOndemandAutoscaleNode
		if o.option.OptimizeAutoscaleOndemandNode {
			targetNodeNames = append(targetNodeNames, targetOndemandAutoscaleNode.Name)
		}
	}

	// Refresh target nodes
	if len(targetNodeNames) == 0 {
		log.Info("Refresh target node does not exist")
		return nil
	}
	evictedPods, err := o.client.RefreshNodes(ctx, targetNodeNames)
	o.result.EvictedPods = evictedPods // update evicted pods
	if err != nil {
		return fmt.Errorf("failed to refresh nodes: %s", err)
	}
	log.Info("Succeeded in refresh nodes")

	// Finish
	return nil
}
