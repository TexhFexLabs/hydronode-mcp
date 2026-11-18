package tools

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type setValueIn struct {
	PointID     string `json:"point_id" jsonschema:"writable data point id (get_sensor shows writable: true)"`
	Value       string `json:"value" jsonschema:"new value as text: true or false (also on/off) for switches, a number like 21.5, or an option text"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"wait up to this many seconds (max 20) for the device to confirm"`
}

type commandIn struct {
	SensorID string `json:"sensor_id" jsonschema:"sensor id"`
	Name     string `json:"name" jsonschema:"command name the device listens to, e.g. relay1"`
	Type     string `json:"type" jsonschema:"BOOL, INT32, UINT32, INT64, UINT64 or STRING (no floats: send an integer in a fixed unit)"`
	Value    string `json:"value" jsonschema:"value as text, e.g. true, 1400, 0x1F"`
}

type logValueIn struct {
	PointID string `json:"point_id" jsonschema:"manual data point id (get_sensor shows manual: true)"`
	Value   string `json:"value" jsonschema:"measured value as text, e.g. 6.8 (true/false or an option text for switch or choice points)"`
	At      string `json:"at,omitempty" jsonschema:"when it was measured: ISO time (default now)"`
}

type markIn struct {
	MessageIDs []string `json:"message_ids,omitempty" jsonschema:"one message id from list_messages (one per call)"`
	All        bool     `json:"all,omitempty" jsonschema:"mark every message as read (action must be read)"`
	Action     string   `json:"action" jsonschema:"read, unread, archive or restore"`
}

type toggleIn struct {
	AutomationID string `json:"automation_id" jsonschema:"automation id from list_automations"`
	Enabled      bool   `json:"enabled" jsonschema:"true to switch on, false to switch off"`
}

type createIn struct {
	Definition map[string]any `json:"definition" jsonschema:"automation as for POST /api/automations/v1: name, triggerType (THRESHOLD on readings, ANOMALY, OFFLINE; required), sensorId, conditions (measurementType or dataPointId, comparison ABOVE/BELOW/IS_ON/EQUALS/CHANGED, threshold, hysteresis), match ALL/ANY, holdSeconds, schedule, actions (NOTIFICATION, NOTIFY_CHANNEL with channelId, WRITE_DATA_POINT with dataPointId and value, COMMAND_TEMPLATE). WEBHOOK is not allowed."`
	DryRun     *bool          `json:"dry_run,omitempty" jsonschema:"default true: only check and preview. Set false to create after the user agreed."`
}

const scopeWrite, scopeAutomations = "hydronode:write", "hydronode:automations"

func registerWrite(s *mcp.Server, d Deps) {
	add(s, d, &mcp.Tool{Name: "set_value", Annotations: rw("Set a value", true, false),
		Description: "Switch or set a writable data point (relay, setpoint, mode). The device gets it on its next contact; wait_seconds waits for confirmation. Ask the user first."},
		func(in setValueIn) (apiCall, error) {
			pid, err := id("point_id", in.PointID)
			if err != nil {
				return apiCall{}, err
			}
			if strings.TrimSpace(in.Value) == "" {
				return apiCall{}, userErr("value is required.")
			}
			w := min(max(in.WaitSeconds, 0), 20)
			body := map[string]any{"value": typedValue(in.Value)}
			if w > 0 {
				body["waitSeconds"] = w
			}
			return apiCall{method: http.MethodPost, path: api + "points/" + pid + "/value", body: body, scope: scopeWrite}, nil
		},
		func(m map[string]any) string {
			switch str(m["state"]) {
			case "CONFIRMED":
				return "The device confirmed the new value."
			case "FAILED":
				return "The device did not take the value." + reason(m)
			case "PENDING":
				return "Sent. The device takes it on its next contact."
			}
			return "Sent."
		})

	add(s, d, &mcp.Tool{Name: "send_command", Annotations: rw("Send a command", true, false),
		Description: "Queue a typed command for a device; it is delivered on the device's next contact. Ask the user first."},
		func(in commandIn) (apiCall, error) {
			sid, err := id("sensor_id", in.SensorID)
			if err != nil {
				return apiCall{}, err
			}
			switch in.Type {
			case "BOOL", "INT32", "UINT32", "INT64", "UINT64", "STRING":
			default:
				return apiCall{}, userErr("type must be BOOL, INT32, UINT32, INT64, UINT64 or STRING.")
			}
			if in.Name == "" || len(in.Name) > 64 || len(in.Value) > 256 {
				return apiCall{}, userErr("name must be 1 to 64 characters and value at most 256.")
			}
			return apiCall{method: http.MethodPost, path: api + "sensors/" + sid + "/commands",
				body: map[string]any{"name": in.Name, "type": in.Type, "value": in.Value}, scope: scopeWrite}, nil
		},
		func(m map[string]any) string { return fmt.Sprintf("Command queued (%s).", lower(m["status"])) })

	add(s, d, &mcp.Tool{Name: "log_value", Annotations: rw("Log a measurement", false, false),
		Description: "Record a value measured by hand (e.g. pH from a test strip) on a manual data point."},
		func(in logValueIn) (apiCall, error) {
			pid, err := id("point_id", in.PointID)
			if err != nil {
				return apiCall{}, err
			}
			if strings.TrimSpace(in.Value) == "" {
				return apiCall{}, userErr("value is required.")
			}
			body := map[string]any{"value": typedValue(in.Value)}
			if in.At != "" {
				if _, err := timeArg("at", in.At); err != nil {
					return apiCall{}, err
				}
				body["at"] = in.At
			}
			return apiCall{method: http.MethodPost, path: api + "points/" + pid + "/manual", body: body, scope: scopeWrite}, nil
		},
		func(map[string]any) string { return "Value logged." })

	add(s, d, &mcp.Tool{Name: "mark_messages", Annotations: rw("Mark messages", false, true),
		Description: "Mark inbox messages as read or unread, archive or restore them, or mark all as read."},
		func(in markIn) (apiCall, error) {
			switch in.Action {
			case "read", "unread", "archive", "restore":
			default:
				return apiCall{}, userErr("action must be read, unread, archive or restore.")
			}
			if in.All {
				if in.Action != "read" || len(in.MessageIDs) > 0 {
					return apiCall{}, userErr("all only works with action read and without message_ids.")
				}
				return apiCall{method: http.MethodPost, path: api + "messages/read-all", scope: scopeWrite}, nil
			}
			if len(in.MessageIDs) != 1 {
				return apiCall{}, userErr("Give exactly one message id per call, or all with action read.")
			}
			mid, err := id("message_ids", in.MessageIDs[0])
			if err != nil {
				return apiCall{}, err
			}
			return apiCall{method: http.MethodPost, path: api + "messages/" + mid + "/" + in.Action, scope: scopeWrite}, nil
		},
		func(m map[string]any) string {
			if k, ok := m["marked"]; ok {
				return plural(n(k), "message marked as read", "messages marked as read") + "."
			}
			return "Done."
		})

	add(s, d, &mcp.Tool{Name: "toggle_automation", Annotations: rw("Switch an automation", true, true),
		Description: "Switch an automation on or off."},
		func(in toggleIn) (apiCall, error) {
			aid, err := id("automation_id", in.AutomationID)
			return apiCall{method: http.MethodPut, path: api + "automations/" + aid + "/enabled",
				body: map[string]any{"enabled": in.Enabled}, scope: scopeWrite}, err
		},
		func(m map[string]any) string {
			if b, _ := m["enabled"].(bool); b {
				return "Automation is on."
			}
			return "Automation is off."
		})

	add(s, d, &mcp.Tool{Name: "create_automation", Annotations: rw("Create an automation", true, false),
		Description: "Check and preview a new automation (dry_run, default), then create it with dry_run false once the user agreed. Only notifications, notification channels, data point writes and command templates; no webhooks."},
		func(in createIn) (apiCall, error) {
			if in.Definition == nil {
				return apiCall{}, userErr("definition is required.")
			}
			dry := in.DryRun == nil || *in.DryRun
			q := map[string]string{"dryRun": "false"}
			if dry {
				q["dryRun"] = "true"
			}
			return apiCall{method: http.MethodPost, path: api + "automations", query: q, body: in.Definition, scope: scopeAutomations}, nil
		},
		func(m map[string]any) string {
			if v, _ := m["valid"].(bool); !v {
				if _, ok := m["valid"]; ok {
					return "Not valid yet. See problems."
				}
			}
			if _, ok := m["id"]; ok {
				return "Automation created: " + str(m["preview"])
			}
			return "Preview: " + str(m["preview"])
		})
}

func reason(m map[string]any) string {
	if r := str(m["reason"]); r != "" {
		return " Reason: " + r + "."
	}
	return ""
}

// typedValue turns the text a client sends into what the Agent API expects: a switch state, a
// number, or the option text. Directories want one declared type per parameter, so the tools take
// text and convert here.
func typedValue(raw string) any {
	v := strings.TrimSpace(raw)
	switch strings.ToLower(v) {
	case "true", "on":
		return true
	case "false", "off":
		return false
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
		return n
	}
	return v
}
