package main

import (
	"context"
	"os"

	"github.com/na-ga/gke-node-optimizer/gke"
	"github.com/na-ga/gke-node-optimizer/log"
	"github.com/na-ga/gke-node-optimizer/report"
	"github.com/na-ga/gke-node-optimizer/service"

	"github.com/kelseyhightower/envconfig"
)

type (
	//
	configuration struct {
		ProjectID                     string `envconfig:"PROJECT_ID" required:"true"`
		ClusterName                   string `envconfig:"CLUSTER_NAME" required:"true"`
		ClusterLocation               string `envconfig:"CLUSTER_LOCATION" required:"true"`
		UseLocalKubeConfig            bool   `envconfig:"USE_LOCAL_KUBE_CONFIG" default:"false"`
		MinimumPreemptibleNodeCount   int    `envconfig:"MINIMUM_PREEMPTIBLE_NODE_COUNT"`
		OptimizePreemptibleNode       bool   `envconfig:"OPTIMIZE_PREEMPTIBLE_NODE" default:"true"`
		OptimizeAutoscaleOndemandNode bool   `envconfig:"OPTIMIZE_AUTOSCALE_ONDEMAND_NODE" default:"true"`
		SlackBotToken                 string `envconfig:"SLACK_BOT_TOKEN"`
		SlackChannelID                string `envconfig:"SLACK_CHANNEL_ID"`
	}
)

const (
	envPrefix = "GNO"
)

//
func main() {

	//
	var conf configuration
	if err := envconfig.Process(envPrefix, &conf); err != nil {
		log.Errorf("Failed to process env var: %s", err)
		os.Exit(1)
	}

	//
	result := report.NewResult(conf.ProjectID)
	var reporter report.Reporter
	if conf.SlackBotToken == "" || conf.SlackChannelID == "" {
		reporter = report.NewReporter()
	} else {
		reporter = report.NewSlackReporter(conf.SlackBotToken, conf.SlackChannelID)
	}

	//
	ctx := context.Background()
	gkeClient, err := gke.New(ctx, conf.ProjectID, conf.ClusterName, conf.ClusterLocation, conf.UseLocalKubeConfig)
	if err != nil {
		log.Errorf("Failed to create gke client: %s", err)
		if e := reporter.Report(result.SetError(err)); e != nil {
			log.Errorf("Failed to post error report: %s", e)
		}
		os.Exit(1)
	}

	//
	log.Info("Start gke node optimizer")
	option := service.OptimizerOption{
		MinimumPreemptibleNodeCount:   conf.MinimumPreemptibleNodeCount,
		OptimizePreemptibleNode:       conf.OptimizePreemptibleNode,
		OptimizeAutoscaleOndemandNode: conf.OptimizeAutoscaleOndemandNode,
	}
	if err := service.NewOptimizer(gkeClient, result, option).Optimize(ctx); err != nil {
		log.Errorf("Failed to gke node optimizer: %s", err)
		if e := reporter.Report(result.SetError(err)); e != nil {
			log.Errorf("Failed to post error report: %s", e)
		}
		os.Exit(1)
	}

	//
	log.Info("Succeeded in gke node optimizer")
	if err := reporter.Report(result); err != nil {
		log.Errorf("Failed to post success report: %s", err)
	}
}
