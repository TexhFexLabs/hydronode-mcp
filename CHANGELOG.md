# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Access log names the JSON-RPC method, tool or prompt, `initialize` client, Cloudflare country and a
  short user agent; a `tool start` line marks that a tool handler ran. Arguments are never logged.
- Stateless Streamable HTTP MCP server (`POST /mcp`) and RFC 9728 Protected Resource Metadata
  (`GET /.well-known/oauth-protected-resource/mcp`).
- 19 MCP tools across read (`overview`, `find`, `get_sensor`, `summarize_history`, `find_periods`,
  `compare`, `list_anomalies`, `get_anomaly`, `list_messages`, `device_health`, `console`,
  `public_stations`, `list_automations`), write (`set_value`, `send_command`, `log_value`,
  `mark_messages`, `toggle_automation`) and automations (`create_automation`).
- 4 MCP prompts (`daily_report`, `why_offline`, `do_i_need_to_water`, `compare_weeks`).
- OAuth 2.1 step-up responses (`403` with `WWW-Authenticate: Bearer error="insufficient_scope"`) and
  in-band guidance linking to `/account/security#connected-apps`.
- Hardened backend client (`hoa_` prefix check, 30 s `/api/agent/v1/me` hash cache, path segment
  validation, no redirects, 64 KiB body cap, per-token rate limit, Origin allowlist).
