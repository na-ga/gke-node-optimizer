package report

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/na-ga/gke-node-optimizer/gke"
)

const (
	ColorCodeRed    = "#FF0000"
	ColorCodeOrange = "#FFA500"
	ColorCodeYellow = "#FFFF00"
	ColorCodeGreen  = "#00FF00"
	ColorCodeBlue   = "#0000FF"
)

// TODO: setting timezone
var timeZone = time.FixedZone("JST", 9*60*60)

//
type Reporter interface {
	// Report reports the result.
	Report(result *Result) error
}

//
type reporter struct {
}

//
func NewReporter() Reporter {
	return &reporter{}
}

//
func (r *reporter) Report(result *Result) error {
	return nil
}

//
type Result struct {
	projectID                   string
	hostname                    string
	startTime                   time.Time
	Error                       error
	Cluster                     *gke.Cluster
	PreemptibleNodeActualCount  int
	PreemptibleNodeMinimumCount int
	ActiveNodePools             []*gke.NodePool
	ActiveNodes                 []*gke.Node
	TargetPreemptibleNode       *gke.Node
	TargetOndemandAutoscaleNode *gke.Node
	EvictedPods                 []*gke.Pod
}

//
func NewResult(projectID string) *Result {
	hostname, _ := os.Hostname()
	return &Result{
		projectID: projectID,
		hostname:  hostname,
		startTime: time.Now(),
	}
}

//
func (r *Result) SetError(err error) *Result {
	r.Error = err
	return r
}

//
func (r *Result) GetDetailLinks() string {
	if !strings.HasPrefix(r.hostname, "gke-node-optimizer-") {
		return ""
	}
	if r.Cluster == nil {
		return ""
	}
	return "https://console.cloud.google.com/logs/query;query=resource.type%3D%22k8s_container%22%0A" +
		"resource.labels.cluster_name%3D%22" + r.Cluster.Name + "%22%0A" +
		"resource.labels.pod_name%3D%22" + r.hostname + "%22%0A" +
		"timestamp%3E%3D%22" + r.startTime.Format(time.RFC3339) + "%22;summaryFields=:true:32:beginning?project=" + r.projectID
}

//
func (r *Result) GetEvictedPodsByNodeName(nodeName string) []*gke.Pod {
	ret := make([]*gke.Pod, 0, len(r.EvictedPods))
	for _, v := range r.EvictedPods {
		if v.NodeName == nodeName {
			ret = append(ret, v)
		}
	}
	return ret
}

// example: returns "03h" when input is "3h23m16.371753687s"
func shortDurationString(duration time.Duration) string {
	if d := duration / (time.Hour * 24); d > 0 {
		return fmt.Sprintf("%02dd", d)
	}
	if h := duration / time.Hour; h > 0 {
		return fmt.Sprintf("%02dh", h)
	}
	if m := duration / time.Minute; m > 0 {
		return fmt.Sprintf("%02dm", m)
	}
	return fmt.Sprintf("%02ds", duration/time.Second)
}

//
func shortText(text string, max int) string {
	if len(text) < max {
		return text
	}
	return text[:max] + "…"
}
