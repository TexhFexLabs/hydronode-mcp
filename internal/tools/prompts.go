package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type prompt struct {
	name, title, description string
	args                     []*mcp.PromptArgument
	text                     func(map[string]string) string
}

func clean(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > 80 {
		v = v[:80]
	}
	return v
}

var prompts = []prompt{
	{
		name: "daily_report", title: "Daily report",
		description: "What happened in the last 24 hours and what needs attention.",
		text: func(map[string]string) string {
			return "Give me a short daily report of my HydroNode setup. Call overview, then device_health and " +
				"list_anomalies (open). For anything notable use summarize_history over 24h. Keep it to a few " +
				"bullet points: what is fine, what changed, what I should do today."
		},
	},
	{
		name: "why_offline", title: "Why is a device offline",
		description: "Find out why a device stopped sending.",
		args:        []*mcp.PromptArgument{{Name: "device", Description: "device name", Required: true}},
		text: func(a map[string]string) string {
			return fmt.Sprintf("Why is my device %q offline or not sending? Use find to get its id, then get_sensor "+
				"(status, battery, signal, last data) and console with rejected_only to see rejected requests. "+
				"Explain the most likely cause and the next step.", clean(a["device"]))
		},
	},
	{
		name: "do_i_need_to_water", title: "Do I need to water",
		description: "Check soil moisture and the recent trend.",
		args:        []*mcp.PromptArgument{{Name: "plant", Description: "plant, bed or sensor name", Required: false}},
		text: func(a map[string]string) string {
			target := "my plants"
			if p := clean(a["plant"]); p != "" {
				target = fmt.Sprintf("%q", p)
			}
			return fmt.Sprintf("Do I need to water %s? Find the soil moisture data points, use summarize_history for "+
				"the last 3 days and find_periods below a sensible threshold. Answer yes or no with one sentence why.", target)
		},
	},
	{
		name: "compare_weeks", title: "Compare this week with last week",
		description: "Compare a value this week with the week before.",
		args:        []*mcp.PromptArgument{{Name: "value", Description: "what to compare, e.g. greenhouse temperature", Required: true}},
		text: func(a map[string]string) string {
			return fmt.Sprintf("Compare %q this week with last week. Use find to get the data point, then compare with "+
				"from 7d and shift 7d. Summarize the difference in average, minimum and maximum in two sentences.", clean(a["value"]))
		},
	},
}

func registerPrompts(s *mcp.Server) {
	for _, p := range prompts {
		p := p
		s.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.description, Arguments: p.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				var args map[string]string
				if req != nil && req.Params != nil {
					args = req.Params.Arguments
				}
				for _, a := range p.args {
					if a.Required && clean(args[a.Name]) == "" {
						return nil, fmt.Errorf("argument %s is required", a.Name)
					}
				}
				return &mcp.GetPromptResult{
					Description: p.description,
					Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text(args)}}},
				}, nil
			})
	}
}
