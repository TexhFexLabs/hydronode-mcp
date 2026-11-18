# hydronode-mcp — notes for coding agents

Hosted MCP server of HydroNode (`hnmcp`), live at `https://hydronode.tech/mcp` since 2026-11-18. Read `README.md` first.

## Rules

- Go, go-sdk for MCP, stateless Streamable HTTP with JSON answers. No database, no secrets, no local mode.
- Accept only `hoa_` agent tokens; verify with `GET /api/agent/v1/me` (cache 30 s, rejected 10 s, one lookup per token).
- Tool answers stay condensed (≤ 48 buckets, ≤ 20 items). Every tool has `title` and `readOnlyHint` or `destructiveHint`
  (directory requirement). Write tools ask the user first (instructions + descriptions).
- Never log tokens, arguments or answers; only tool, status, duration, 12-character token hash.
- Tests: `go test -race ./...`, `staticcheck`, `govulncheck` (see README). Write the test first.
- Commits: short, GPG-signed.

## Release

| What | How |
|---|---|
| Server image | push to `main` (code, not docs) → `release.yml` builds `ghcr.io/texhfexlabs/hydronode-mcp:latest`, signs it with cosign (only from `main`); production pulls signed images within minutes |
| Test environment | push to branch `int` → `int.yml` builds `:int` |
| MCP Registry | bump `version` in `server.json`, `mcp-publisher validate`, log in with the domain key (`docs/REGISTRY.md`), `mcp-publisher publish`. Listed as `tech.hydronode/hydronode` |
| Claude Connectors Directory | texts in `docs/CLAUDE_DIRECTORY.md` |
| End-to-end check | `tools/e2e/mcp_e2e.py` against a running server with a real agent token from a file |

Contracts live in the backend repo (`docs/api/OAUTH.md`, `docs/api/AGENT_API.md`); change them there first.
