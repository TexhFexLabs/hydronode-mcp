package tools

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const api = "/api/agent/v1/"

type noArgs struct{}

type findIn struct {
	Query string `json:"query" jsonschema:"words to search in sensor, data point, station and project names, ids or DevEUIs"`
}

type sensorIn struct {
	SensorID string `json:"sensor_id" jsonschema:"sensor id"`
}

type rangeIn struct {
	From string `json:"from,omitempty" jsonschema:"start: ISO time, date, or relative like 24h, 7d, 30d (default 24h ago)"`
	To   string `json:"to,omitempty" jsonschema:"end: ISO time, date, or relative (default now)"`
}

type summaryIn struct {
	PointID string `json:"point_id" jsonschema:"data point id"`
	rangeIn
}

type periodsIn struct {
	PointID string   `json:"point_id" jsonschema:"data point id"`
	Below   *float64 `json:"below,omitempty" jsonschema:"find periods where the value was below this"`
	Above   *float64 `json:"above,omitempty" jsonschema:"find periods where the value was above this"`
	rangeIn
}

type compareIn struct {
	PointIDs []string `json:"point_ids" jsonschema:"one to four data point ids"`
	Shift    string   `json:"shift,omitempty" jsonschema:"compare with the same span earlier, e.g. 7d (only with one point)"`
	rangeIn
}

type anomaliesIn struct {
	State    string `json:"state,omitempty" jsonschema:"open (default) or all"`
	SensorID string `json:"sensor_id,omitempty" jsonschema:"only this sensor"`
	Since    string `json:"since,omitempty" jsonschema:"only anomalies seen since: ISO time or relative like 7d"`
}

type anomalyIn struct {
	AnomalyID string `json:"anomaly_id" jsonschema:"anomaly id"`
}

type messagesIn struct {
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only unread messages"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"cursor from a previous answer to get more"`
}

type consoleIn struct {
	SensorID     string `json:"sensor_id,omitempty" jsonschema:"only this sensor"`
	RejectedOnly bool   `json:"rejected_only,omitempty" jsonschema:"only requests HydroNode rejected, with the reason"`
	Limit        int    `json:"limit,omitempty" jsonschema:"number of lines, 1 to 50 (default 20)"`
}

type stationsIn struct {
	Query    string  `json:"query,omitempty" jsonschema:"station name to search"`
	Near     string  `json:"near,omitempty" jsonschema:"latitude,longitude, e.g. 52.52,13.40"`
	RadiusKm float64 `json:"radius_km,omitempty" jsonschema:"search radius around near in km, up to 100 (default 10)"`
}

type automationsIn struct {
	AutomationID string `json:"automation_id,omitempty" jsonschema:"show one automation with its recent runs"`
}

func registerRead(s *mcp.Server, d Deps) {
	add(s, d, &mcp.Tool{Name: "overview", Annotations: ro("Overview"),
		Description: "Projects with pools, device counts by status, open anomalies, unread messages and up to 10 things that need attention."},
		func(noArgs) (apiCall, error) { return apiCall{method: http.MethodGet, path: api + "overview"}, nil },
		func(m map[string]any) string {
			parts := []string{plural(n(get(m, "devices", "total")), "device", "devices")}
			if k := n(get(m, "devices", "offline")); k > 0 {
				parts = append(parts, fmt.Sprintf("%d offline", k))
			}
			if k := n(get(m, "devices", "delayed")); k > 0 {
				parts = append(parts, fmt.Sprintf("%d delayed", k))
			}
			parts = append(parts, plural(n(m["openAnomalies"]), "open anomaly", "open anomalies"),
				plural(n(m["unreadMessages"]), "unread message", "unread messages"))
			if na, _ := m["needsAttention"].([]any); len(na) > 0 {
				parts = append(parts, plural(len(na), "item needs attention", "items need attention"))
			}
			return join(parts) + "."
		})

	add(s, d, &mcp.Tool{Name: "find", Annotations: ro("Find"),
		Description: "Find sensors, data points, stations and projects by name, id or DevEUI. Returns up to 5 per group with their ids."},
		func(in findIn) (apiCall, error) {
			q := strings.TrimSpace(in.Query)
			if q == "" || len(q) > 100 {
				return apiCall{}, userErr("query must be 1 to 100 characters.")
			}
			return apiCall{method: http.MethodGet, path: api + "find", query: map[string]string{"q": q}}, nil
		},
		func(m map[string]any) string {
			total := 0
			for _, k := range []string{"sensors", "points", "stations", "projects"} {
				l, _ := m[k].([]any)
				total += len(l)
			}
			if total == 0 {
				return "Nothing found. Try fewer or other words."
			}
			return plural(total, "match", "matches") + "."
		})

	add(s, d, &mcp.Tool{Name: "get_sensor", Annotations: ro("Sensor"),
		Description: "One sensor with status, battery, signal, firmware and every data point (current value, unit, age, writable)."},
		func(in sensorIn) (apiCall, error) {
			sid, err := id("sensor_id", in.SensorID)
			return apiCall{method: http.MethodGet, path: api + "sensors/" + sid}, err
		},
		func(m map[string]any) string {
			pts, _ := m["points"].([]any)
			return fmt.Sprintf("%s is %s, last data %s ago, %s.", quoted(m["name"]), lower(m["status"]), or(str(m["ago"]), "?"), plural(len(pts), "data point", "data points"))
		})

	add(s, d, &mcp.Tool{Name: "summarize_history", Annotations: ro("History summary"),
		Description: "History of one data point over a time range: min and max with time, average, trend per day, gaps, coverage and at most 48 buckets."},
		func(in summaryIn) (apiCall, error) {
			pid, err := id("point_id", in.PointID)
			if err != nil {
				return apiCall{}, err
			}
			q, err := rangeQuery(in.rangeIn)
			return apiCall{method: http.MethodGet, path: api + "points/" + pid + "/summary", query: q}, err
		},
		func(m map[string]any) string {
			if get(m, "min", "value") == nil {
				return "No data in this range."
			}
			u := str(m["unit"])
			return fmt.Sprintf("Min %s%s, max %s%s, average %s%s, coverage %d%%.",
				str(get(m, "min", "value")), sp(u), str(get(m, "max", "value")), sp(u), str(m["avg"]), sp(u), int(fl(m["coverage"])*100))
		})

	add(s, d, &mcp.Tool{Name: "find_periods", Annotations: ro("Find periods"),
		Description: "When was a data point below or above a value? Returns up to 10 periods, their total minutes and share of the range."},
		func(in periodsIn) (apiCall, error) {
			pid, err := id("point_id", in.PointID)
			if err != nil {
				return apiCall{}, err
			}
			if (in.Below == nil) == (in.Above == nil) {
				return apiCall{}, userErr("Give exactly one of below or above.")
			}
			q, err := rangeQuery(in.rangeIn)
			if err != nil {
				return apiCall{}, err
			}
			q["below"], q["above"] = num(in.Below), num(in.Above)
			return apiCall{method: http.MethodGet, path: api + "points/" + pid + "/periods", query: q}, nil
		},
		func(m map[string]any) string {
			p, _ := m["periods"].([]any)
			return fmt.Sprintf("%s, %d minutes in total (%d%% of the range).", plural(len(p)+n(m["more"]), "period", "periods"), n(m["totalMinutes"]), int(fl(m["share"])*100))
		})

	add(s, d, &mcp.Tool{Name: "compare", Annotations: ro("Compare"),
		Description: "Compare up to 4 data points over the same range, or one data point with an earlier span (shift, e.g. 7d for last week)."},
		func(in compareIn) (apiCall, error) {
			if len(in.PointIDs) < 1 || len(in.PointIDs) > 4 {
				return apiCall{}, userErr("point_ids needs 1 to 4 ids.")
			}
			if in.Shift != "" && len(in.PointIDs) != 1 {
				return apiCall{}, userErr("shift works with exactly one point.")
			}
			if len(in.PointIDs) == 1 && in.Shift == "" {
				return apiCall{}, userErr("Give two or more points, or one point and a shift.")
			}
			ids := make([]string, len(in.PointIDs))
			for i, p := range in.PointIDs {
				v, err := id("point_ids", p)
				if err != nil {
					return apiCall{}, err
				}
				ids[i] = v
			}
			q, err := rangeQuery(in.rangeIn)
			if err != nil {
				return apiCall{}, err
			}
			if q["shift"], err = timeArg("shift", in.Shift); err != nil {
				return apiCall{}, err
			}
			q["points"] = strings.Join(ids, ",")
			return apiCall{method: http.MethodGet, path: api + "compare", query: q}, nil
		},
		func(m map[string]any) string {
			sr, _ := m["series"].([]any)
			return fmt.Sprintf("Compared %s.", plural(len(sr), "series", "series"))
		})

	add(s, d, &mcp.Tool{Name: "list_anomalies", Annotations: ro("Anomalies"),
		Description: "Anomalies HydroNode detected (open by default), newest first, up to 20."},
		func(in anomaliesIn) (apiCall, error) {
			q := map[string]string{}
			switch in.State {
			case "", "open", "all":
				q["state"] = in.State
			default:
				return apiCall{}, userErr("state must be open or all.")
			}
			if in.SensorID != "" {
				v, err := id("sensor_id", in.SensorID)
				if err != nil {
					return apiCall{}, err
				}
				q["sensor"] = v
			}
			var err error
			q["since"], err = timeArg("since", in.Since)
			return apiCall{method: http.MethodGet, path: api + "anomalies", query: q}, err
		},
		listSummary("anomalies"))

	add(s, d, &mcp.Tool{Name: "get_anomaly", Annotations: ro("Anomaly"),
		Description: "One anomaly with explanation, expected and observed values and its history."},
		func(in anomalyIn) (apiCall, error) {
			v, err := id("anomaly_id", in.AnomalyID)
			return apiCall{method: http.MethodGet, path: api + "anomalies/" + v}, err
		},
		func(m map[string]any) string { return "Anomaly " + quoted(m["title"]) + "." })

	add(s, d, &mcp.Tool{Name: "list_messages", Annotations: ro("Messages"),
		Description: "Messages in the HydroNode inbox (devices, anomalies, account, news), up to 20."},
		func(in messagesIn) (apiCall, error) {
			q := map[string]string{"unread": boolArg(in.UnreadOnly)}
			if in.Cursor != "" {
				if len(in.Cursor) > 200 {
					return apiCall{}, userErr("cursor is too long.")
				}
				q["cursor"] = in.Cursor
			}
			return apiCall{method: http.MethodGet, path: api + "messages", query: q}, nil
		},
		listSummary("messages"))

	add(s, d, &mcp.Tool{Name: "device_health", Annotations: ro("Device health"),
		Description: "Only devices with a problem: offline, delayed, low battery, failed updates, rejected data."},
		func(noArgs) (apiCall, error) {
			return apiCall{method: http.MethodGet, path: api + "devices/health"}, nil
		},
		func(m map[string]any) string {
			items, _ := m["items"].([]any)
			if len(items) == 0 {
				return "All devices are fine."
			}
			return plural(len(items), "device has a problem", "devices have problems") + "."
		})

	add(s, d, &mcp.Tool{Name: "console", Annotations: ro("Console"),
		Description: "Latest requests HydroNode received from devices (up to 50), with the reason when one was rejected."},
		func(in consoleIn) (apiCall, error) {
			if in.Limit < 0 || in.Limit > 50 {
				return apiCall{}, userErr("limit must be 1 to 50.")
			}
			q := map[string]string{"rejected": boolArg(in.RejectedOnly), "limit": intArg(in.Limit)}
			if in.SensorID != "" {
				v, err := id("sensor_id", in.SensorID)
				if err != nil {
					return apiCall{}, err
				}
				q["sensor"] = v
			}
			return apiCall{method: http.MethodGet, path: api + "console", query: q}, nil
		},
		listSummary("console lines"))

	add(s, d, &mcp.Tool{Name: "public_stations", Annotations: ro("Public stations"),
		Description: "Public weather and air stations with current values, by name or near a location."},
		func(in stationsIn) (apiCall, error) {
			q := map[string]string{}
			if in.Near != "" {
				if !nearRe.MatchString(in.Near) {
					return apiCall{}, userErr("near must be latitude,longitude like 52.52,13.40.")
				}
				q["near"] = in.Near
			}
			if in.RadiusKm < 0 || in.RadiusKm > 100 {
				return apiCall{}, userErr("radius_km must be up to 100.")
			}
			if in.RadiusKm > 0 {
				q["radiusKm"] = num(&in.RadiusKm)
			}
			if qs := strings.TrimSpace(in.Query); qs != "" {
				if len(qs) > 100 {
					return apiCall{}, userErr("query is too long.")
				}
				q["q"] = qs
			}
			return apiCall{method: http.MethodGet, path: api + "stations/public", query: q}, nil
		},
		listSummary("stations"))

	add(s, d, &mcp.Tool{Name: "list_automations", Annotations: ro("Automations"),
		Description: "Automations with trigger, actions, on/off and when they last fired; with automation_id one automation and its recent runs."},
		func(in automationsIn) (apiCall, error) {
			if in.AutomationID == "" {
				return apiCall{method: http.MethodGet, path: api + "automations"}, nil
			}
			v, err := id("automation_id", in.AutomationID)
			return apiCall{method: http.MethodGet, path: api + "automations/" + v}, err
		},
		func(m map[string]any) string {
			if _, ok := m["items"]; ok {
				return listSummary("automations")(m)
			}
			state := "off"
			if b, _ := m["enabled"].(bool); b {
				state = "on"
			}
			return fmt.Sprintf("%s is %s.", quoted(m["name"]), state)
		})
}

func rangeQuery(r rangeIn) (map[string]string, error) {
	from, err := timeArg("from", r.From)
	if err != nil {
		return nil, err
	}
	to, err := timeArg("to", r.To)
	if err != nil {
		return nil, err
	}
	return map[string]string{"from": from, "to": to}, nil
}

func fl(v any) float64 {
	f, _ := v.(float64)
	return f
}

func sp(unit string) string {
	if unit == "" {
		return ""
	}
	return " " + unit
}

func or(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func lower(v any) string {
	return strings.ToLower(strings.ReplaceAll(or(str(v), "unknown"), "_", " "))
}

// quoted marks user text so it never reads as part of our sentence.
func quoted(v any) string {
	t := str(v)
	if len(t) > 60 {
		t = t[:60]
	}
	return fmt.Sprintf("%q", t)
}
