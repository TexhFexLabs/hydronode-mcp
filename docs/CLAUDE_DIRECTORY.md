# Claude Connectors Directory (prepared, not submitted)

Submit after the Prod test (TD 1.84.5) through Anthropic's connector submission form. Check the current form
fields and requirements first; they change. Everything below is ready to paste.

## Listing

| Field | Value |
|---|---|
| Name | HydroNode |
| Server URL | `https://hydronode.tech/mcp` (Streamable HTTP, stateless) |
| Auth | OAuth 2.1, PKCE S256, `resource` (RFC 8707). Clients register by Client ID Metadata Document (Claude's) or DCR. Metadata: `/.well-known/oauth-protected-resource/mcp`, `/.well-known/oauth-authorization-server` |
| Tagline | Ask about your IoT sensors and switch devices with your consent. |
| Description | HydroNode connects ESP32, LoRaWAN, MQTT and other devices. With this connector Claude reads your sensors, history, anomalies, inbox and automations in condensed form (at most 48 buckets, 20 items), diagnoses offline devices, and, only if you allow it, switches outputs, sends device commands, logs manual values and drafts automations. You choose the projects and rights on HydroNode's consent page and can change or revoke them anytime. |
| Category | Productivity / IoT |
| Docs | https://hydronode.tech/docs/guide/mcp |
| Privacy policy | https://hydronode.tech/privacy |
| Terms | https://hydronode.tech/terms |
| Support | contact@hydronode.tech, security: security@hydronode.tech |
| Logo | `docs/assets/logo-light.svg`, `logo-dark.svg` (square PNG 512 px if the form asks) |
| Pricing | free (20 sensors per account) |

## Tools (19, every one with `title` and `readOnlyHint` or `destructiveHint`)

Read (13, `readOnlyHint`): `overview`, `find`, `get_sensor`, `summarize_history`, `find_periods`, `compare`,
`list_anomalies`, `get_anomaly`, `list_messages`, `device_health`, `console`, `public_stations`, `list_automations`.

Write (6, need `hydronode:write`, automations need `hydronode:automations`): `set_value`, `send_command`,
`toggle_automation`, `create_automation` (`destructiveHint`), `log_value`, `mark_messages`. Without the scope the
tool answers with a text and HTTP 403 `insufficient_scope` (step-up). `create_automation` is a dry run by default,
webhooks are never allowed.

Prompts: `daily_report`, `why_offline`, `do_i_need_to_water`, `compare_weeks`.

## Data and safety (for the review questions)

- No data leaves HydroNode except the answers to the tool calls; hnmcp keeps nothing (no database, no logs of
  arguments or answers; logs hold tool name, status, duration and a 12-character token hash).
- Tokens: opaque `hoa_` access (60 min) and rotating `hor_` refresh tokens, stored as SHA-256, scoped to the
  grant's projects. They work only through `/mcp`; elsewhere they are refused.
- Write access needs a sign-in younger than five minutes when granted. Every write is audited with the app name.
- Revoke: https://hydronode.tech/account/security#connected-apps (immediate in the backend, at most 30 s in hnmcp).

## Example prompts (for screenshots and the review)

1. "How are my sensors doing?" → `overview`, `device_health`
2. "How warm was the greenhouse last week, and when was it above 30 °C?" → `find`, `summarize_history`, `find_periods`
3. "Why is the balcony station not sending?" → `get_sensor`, `console`
4. "Turn the pump on." (with write access) → asks, then `set_value`
5. "Mark my messages as read." → `mark_messages`

## Review account

The App Review demo account (same as for the iOS App Review; credentials only in App Store Connect,
"App Review Information"). Email and password, no MFA. Contents (2026-11-18):

- Project "My project" with 5 live sensors and at least 7 days of history: Greenhouse, Living Room Air,
  Solar Battery, Water Tank, Rooftop Weather.
- "Irrigation pump" on Greenhouse: writable switch (`set_value`).
- "Hand measurements" with "Nutrient pH": manual data point (`log_value`).
- Automation "Greenhouse too hot" (off, so `toggle_automation` can be tried).
- Unread messages in the inbox (`list_messages`, `mark_messages`).

Reset after the review if reviewers changed things.
