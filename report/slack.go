package report

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

const bulkMaxLength = 20

//
type slackReporter struct {
	cli       *slack.Client
	channelID string
}

//
func NewSlackReporter(token, channelID string) Reporter {
	return &slackReporter{
		cli:       slack.New(token),
		channelID: channelID,
	}
}

//
func (s *slackReporter) Report(result *Result) error {

	//
	color := ColorCodeGreen
	title := "Succeeded in optimize gke cluster nodes."
	message := "All tasks has been completed"
	if result.Error != nil {
		color = ColorCodeRed
		title = "Failed to optimize gke cluster nodes."
		message = result.Error.Error()
	} else if result.TargetOndemandAutoscaleNode != nil {
		color = ColorCodeOrange
		title = "Succeeded in optimize gke cluster nodes, but there are some things to check."
		message = "All tasks has been completed. However uses autoscale nodes. Check the capacity is sufficient."
	}
	if detail := result.GetDetailLinks(); detail != "" {
		title += " " + s.WrapTextInLink("More detail information.", detail)
	}

	//
	clusterNameLink := "unknown"
	if result.Cluster != nil {
		clusterNameLink = s.WrapTextInLink(result.Cluster.Name, result.Cluster.ResourceURL)
	}
	activeNodePoolNameLinks := []string{"none"}
	if len(result.ActiveNodePools) > 0 {
		activeNodePoolNameLinks = make([]string, len(result.ActiveNodePools))
		for i, v := range result.ActiveNodePools {
			activeNodePoolNameLinks[i] = fmt.Sprintf("- %02d: %s (autoscale=%t)", i+1, s.WrapTextInLink(v.Name, v.ResourceURL), v.Autoscale)
		}
	}
	activeNodeNameLinks := []string{"none"}
	if len(result.ActiveNodes) > 0 {
		activeNodeNameLinks = make([]string, len(result.ActiveNodes))
		for i, v := range result.ActiveNodes {
			extra := fmt.Sprintf("(age=%s, pods=%02d)", shortDurationString(v.Age), len(v.Pods))
			activeNodeNameLinks[i] = fmt.Sprintf("- %02d: %s %s", i+1, v.Name, extra)
		}
	}
	var targetPreemptibleNode []string
	if result.TargetPreemptibleNode != nil {
		evictedPods := result.GetEvictedPodsByNodeName(result.TargetPreemptibleNode.Name)
		targetPreemptibleNode = make([]string, 0, len(evictedPods)+1)
		extra := fmt.Sprintf("(age=%s, pods=%02d)", shortDurationString(result.TargetPreemptibleNode.Age), len(result.TargetPreemptibleNode.Pods))
		targetPreemptibleNode = append(targetPreemptibleNode, result.TargetPreemptibleNode.Name+" "+extra)
		for i, v := range evictedPods {
			targetPreemptibleNode = append(targetPreemptibleNode, fmt.Sprintf("- %02d: %s (ns=%s)", i+1, shortText(v.Name, 40), v.Namespace))
		}
	}
	var targetOndemandAutoscaleNode []string
	if result.TargetOndemandAutoscaleNode != nil {
		evictedPods := result.GetEvictedPodsByNodeName(result.TargetOndemandAutoscaleNode.Name)
		targetOndemandAutoscaleNode = make([]string, 0, len(evictedPods)+1)
		extra := fmt.Sprintf("(age=%s, pods=%02d)", shortDurationString(result.TargetOndemandAutoscaleNode.Age), len(result.TargetOndemandAutoscaleNode.Pods))
		targetOndemandAutoscaleNode = append(targetOndemandAutoscaleNode, result.TargetOndemandAutoscaleNode.Name+" "+extra)
		for i, v := range evictedPods {
			targetOndemandAutoscaleNode = append(targetOndemandAutoscaleNode, fmt.Sprintf("- %02d: %s (ns=%s)", i+1, shortText(v.Name, 40), v.Namespace))
		}
	}

	//
	fields := []slack.AttachmentField{
		{
			Title: "Cluster name",
			Value: clusterNameLink,
			Short: true,
		},
		{
			Title: "Cluster nodes count",
			Value: fmt.Sprintf("%d", len(result.ActiveNodes)),
			Short: true,
		},
		{
			Title: "Preemptible nodes count",
			Value: fmt.Sprintf("%d", result.PreemptibleNodeActualCount),
			Short: true,
		},
		{
			Title: "Preemptible nodes minimum count",
			Value: fmt.Sprintf("%d", result.PreemptibleNodeMinimumCount),
			Short: true,
		},
		{
			Title: "Optimize start time",
			Value: result.startTime.In(timeZone).Format(time.RFC3339),
			Short: true,
		},
		{
			Title: "Optimize end time",
			Value: time.Now().In(timeZone).Format(time.RFC3339),
			Short: true,
		},
	}

	//
	fields = s.appendField(fields, "Active node pools", activeNodePoolNameLinks)
	fields = s.appendField(fields, "Active nodes", activeNodeNameLinks)
	fields = s.appendField(fields, "Refresh target preemptible node", targetPreemptibleNode)
	fields = s.appendField(fields, "Refresh target ondemand auto scale node", targetOndemandAutoscaleNode)

	//
	if message != "" {
		fields = append(fields, slack.AttachmentField{
			Title: "Message",
			Value: s.WrapTextInCodeBlock(message),
		})
	}

	//
	opts := []slack.MsgOption{
		slack.MsgOptionAsUser(true),
		slack.MsgOptionDisableLinkUnfurl(),
		slack.MsgOptionText(title, false),
		slack.MsgOptionAttachments(slack.Attachment{
			Fields: fields,
			Color:  color,
		}),
	}
	_, _, err := s.cli.PostMessage(s.channelID, opts...)
	return err
}

//
func (s *slackReporter) appendField(fields []slack.AttachmentField, title string, values []string) []slack.AttachmentField {
	if len(values) == 0 {
		return fields
	}
	if len(values) < bulkMaxLength {
		fields = append(fields, slack.AttachmentField{
			Title: title,
			Value: s.WrapTextsInCodeBlock(values),
		})
		return fields
	}
	idx := 1
	pages := int(math.Ceil(float64(len(values)) / float64(bulkMaxLength)))
	bulkMessages := make([]string, 0, bulkMaxLength)
	for _, v := range values {
		bulkMessages = append(bulkMessages, v)
		// add field if bulk messages length is equals to bulk post max length.
		if len(bulkMessages) == bulkMaxLength {
			fields = append(fields, slack.AttachmentField{
				Title: fmt.Sprintf("%s (%d/%d)", title, idx, pages),
				Value: s.WrapTextsInCodeBlock(bulkMessages),
			})
			idx++                                           // increment
			bulkMessages = make([]string, 0, bulkMaxLength) // clear
		}
	}
	if len(bulkMessages) > 0 {
		fields = append(fields, slack.AttachmentField{
			Title: fmt.Sprintf("%s (%d/%d)", title, idx, pages),
			Value: s.WrapTextsInCodeBlock(bulkMessages),
		})
	}
	return fields
}

// WrapTextInCodeBlock wraps a string into a code-block formatted string
func (s *slackReporter) WrapTextInCodeBlock(text string) string {
	return fmt.Sprintf("```\n%s\n```", text)
}

// WrapTextsInCodeBlock wraps are strings into a code-block formatted string
func (s *slackReporter) WrapTextsInCodeBlock(texts []string) string {
	return fmt.Sprintf("```\n%s\n```", strings.Join(texts, "\n"))
}

// WrapTextInInlineCodeBlock wraps a string into a inline-code-block formatted string
func (s *slackReporter) WrapTextInInlineCodeBlock(text string) string {
	return fmt.Sprintf("`%s`", text)
}

// WrapUserNameInLink converts to a linkable user name.
func (s *slackReporter) WrapUserNameInLink(userName string) string {
	return fmt.Sprintf("<@%s>", userName)
}

// WrapTextInLink converts to a linkable string.
func (s *slackReporter) WrapTextInLink(des, link string) string {
	return fmt.Sprintf("<%s|%s>", link, des)
}
