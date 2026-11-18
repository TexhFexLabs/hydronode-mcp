<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img src="docs/assets/logo-light.svg" alt="HydroNode" width="96" height="96">
  </picture>
</p>

<h1 align="center">hydronode-mcp</h1>

<p align="center">
  Hosted Model Context Protocol (MCP) server for HydroNode IoT sensors, time series and controls.
</p>

<p align="center">
  <a href="https://github.com/TexhFexLabs/hydronode-mcp/actions/workflows/ci.yml"><img src="https://github.com/TexhFexLabs/hydronode-mcp/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
  <img src="https://img.shields.io/badge/go-1.24%2B-00ADD8.svg" alt="Go 1.24+">
  <img src="https://img.shields.io/badge/MCP-Streamable%20HTTP-6e56cf.svg" alt="MCP Streamable HTTP">
</p>

Ask Claude, ChatGPT, Cursor, VS Code or Codex about your HydroNode sensors in plain language, and let
them switch outputs, send commands, tick off messages or draft automations with the rights you grant.

## Connect in 30 seconds

**Hosted endpoint:** `https://hydronode.tech/mcp`

| Client | Setup |
|---|---|
| **Cursor** | [Add to Cursor](cursor://anysphere.cursor-deeplink/mcp/install?name=hydronode&config=eyJ1cmwiOiJodHRwczovL2h5ZHJvbm9kZS50ZWNoL21jcCJ9) or add `{"mcpServers":{"hydronode":{"url":"https://hydronode.tech/mcp"}}}` to `.cursor/mcp.json` |
| **VS Code** | [Install in VS Code](vscode:mcp/install?%7B%22name%22%3A%22hydronode%22%2C%22type%22%3A%22http%22%2C%22url%22%3A%22https%3A%2F%2Fhydronode.tech%2Fmcp%22%7D) or add `{"servers":{"hydronode":{"type":"http","url":"https://hydronode.tech/mcp"}}}` to `.vscode/mcp.json` |
| **Claude.ai / Claude Desktop** | Settings → Connectors → Add custom connector → `https://hydronode.tech/mcp` |
| **Claude Code** | `claude mcp add --transport http hydronode https://hydronode.tech/mcp` |
| **ChatGPT** | Settings → Connectors → Add MCP connector → `https://hydronode.tech/mcp` |
| **Codex** | Add `[mcp_servers.hydronode]` with `url = "https://hydronode.tech/mcp"` to `~/.codex/config.toml` |
| **Claude plugin** (connector + skills) | `claude plugin marketplace add TexhFexLabs/hydronode-skills`, then `claude plugin install hydronode@hydronode` |
| **Clients without remote MCP** | run the bridge `npx -y mcp-remote https://hydronode.tech/mcp` as a local stdio server (needs Node.js) |

On first use your browser opens HydroNode's OAuth 2.1 consent page so you can choose which projects
the assistant may see and whether it gets read-only access (`hydronode:read`) or edit access
(`hydronode:write`, `hydronode:automations`). Manage or revoke connected apps anytime under
`https://hydronode.tech/account/security#connected-apps`.

## Architecture

```
Claude / ChatGPT ──HTTPS──► <host>/mcp ──Traefik──► hnmcp (this repo: stateless, no DB, no secrets)
                                                      │ Agent API with the user's own token
                                                      ▼
                                                   hnbackend (http://hnbackend:8080, internal only)
```

- **Hosted only.** Remote MCP over Streamable HTTP at `https://<host>/mcp`, stateless, JSON answers. No local mode.
- **OAuth 2.1 only.** Clients discover the HydroNode authorization server via RFC 9728 metadata, register
  themselves (DCR or Client ID Metadata Document) and the user picks scopes and projects on HydroNode's consent page.
- **Condensed answers.** History comes as ≤ 48 buckets with min/avg/max, lists as ≤ 20 items. Never raw dumps.
- **Isolated.** `hnmcp` has no database access and no server secrets. It forwards the caller's agent token
  (`hoa_…`) to the backend's Agent API over the internal network. Agent tokens work nowhere else: Traefik
  blocks them on every public route except `/mcp`, and the backend only accepts them without proxy headers.

Contracts: `hydronode-backend/docs/api/OAUTH.md`, `hydronode-backend/docs/api/AGENT_API.md`.

## Tools (19)

| Tool | Kind | What it does |
|---|---|---|
| `overview` | read | Projects, device counts by status, open anomalies, unread messages, what needs attention |
| `find` | read | Sensors, data points, stations, projects by name, id or DevEUI |
| `get_sensor` | read | One sensor with status, battery, signal, firmware and every data point |
| `summarize_history` | read | Min/max with time, average, trend, gaps, ≤ 48 buckets |
| `find_periods` | read | When a value was below/above a threshold |
| `compare` | read | Up to 4 data points, or one point against an earlier span (`shift`) |
| `list_anomalies` / `get_anomaly` | read | Detected anomalies and their explanation |
| `list_messages` | read | Inbox |
| `device_health` | read | Only devices with problems |
| `console` | read | Latest device requests with rejection reasons |
| `public_stations` | read | Public stations by name or location |
| `list_automations` | read | Automations, one with recent runs |
| `set_value` | write | Switch or set a writable data point (destructive hint) |
| `send_command` | write | Queue a typed device command (destructive hint) |
| `log_value` | write | Record a manual measurement |
| `mark_messages` | write | Read, unread, archive, restore, read all |
| `toggle_automation` | write | Automation on/off |
| `create_automation` | automations | Dry run by default; no webhooks ever |

Write tools are always listed. Without the scope they answer with a clear text and a link to Connected apps,
and the HTTP answer is `403` with `WWW-Authenticate: Bearer error="insufficient_scope", scope="…"` so clients
that support step-up reopen the consent (switch `HNMCP_STEPUP_HTTP`).

Prompts: `daily_report`, `why_offline`, `do_i_need_to_water`, `compare_weeks`.

## Endpoints

| Path | |
|---|---|
| `POST /mcp` | MCP (Streamable HTTP, stateless). Needs `Authorization: Bearer hoa_…` |
| `GET /.well-known/oauth-protected-resource/mcp` | RFC 9728 metadata |
| `GET /q/health/live`, `/q/health/ready` | health (ready also checks the backend) |

## Configuration

| Env | Default | |
|---|---|---|
| `HNMCP_LISTEN` | `:8080` | listen address |
| `HNMCP_BACKEND_URL` | required | internal backend, e.g. `http://hnbackend:8080` |
| `HNMCP_PUBLIC_URL` | required | public origin, e.g. `https://hydronode.tech` (resource = `+/mcp`) |
| `HNMCP_AUTH_SERVER` | `HNMCP_PUBLIC_URL` | OAuth issuer |
| `HNMCP_ALLOWED_ORIGINS` | `https://claude.ai,https://claude.com,https://chatgpt.com` | browser origins allowed on `/mcp` (requests without `Origin` always pass) |
| `HNMCP_RATE_PER_MIN` | `120` | requests per token per minute (the backend has its own limits) |
| `HNMCP_STEPUP_HTTP` | `true` | answer missing scopes with HTTP 403 step-up |
| `HNMCP_LOG_LEVEL` | `INFO` | |

## Security

- Accepts only `hoa_` agent tokens; PATs, refresh tokens and JWTs are refused without a backend call.
- Token check via `GET /api/agent/v1/me`, cached 30 s by SHA-256 hash (rejected tokens 10 s, one lookup for
  concurrent requests); revocation applies within 30 s here and immediately in the backend.
- Only Agent API paths can be called, ids are validated, redirects are never followed.
- Origin allowlist, 64 KiB body limit, 20 s per request, per-token rate limit, at most 300 rejected tokens per
  minute and address (above that only already verified tokens pass).
- Logs carry tool name, status, duration and a 12-character token hash. Never tokens, arguments or answers.
- distroless nonroot image, read-only filesystem, no capabilities.

## Develop

```bash
go test -race ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# against a local backend (mvn quarkus:dev on :8080, OAuth + Agent API on in %dev)
HNMCP_LISTEN=:8091 HNMCP_BACKEND_URL=http://localhost:8080 \
HNMCP_PUBLIC_URL=http://localhost:8091 HNMCP_AUTH_SERVER=http://localhost:8080 \
HNMCP_ALLOWED_ORIGINS=http://localhost:6274 go run ./cmd/hnmcp
npx @modelcontextprotocol/inspector   # connect to http://localhost:8091/mcp
```

## Release

| Branch | Workflow | Image |
|---|---|---|
| any, PR | `ci.yml` | none (vet, gofmt, staticcheck, race tests, govulncheck) |
| `int` | `int.yml` | `ghcr.io/texhfexlabs/hydronode-mcp:int`, `:int-<sha>` (amd64) |
| `main` (push or manual, only from `main`) | `release.yml` | `:latest`, `:0.1.<run>`, amd64+arm64, cosign keyless; prod timer `hnmcp-autodeploy` swaps it |

Listed in the [MCP Registry](https://registry.modelcontextprotocol.io) as `tech.hydronode/hydronode` (`server.json`,
`docs/REGISTRY.md`). Claude Connectors Directory texts: `docs/CLAUDE_DIRECTORY.md`.

## License

MIT, see [LICENSE](LICENSE).
