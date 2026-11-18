# Security

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability reporting
("Security" tab → "Report a vulnerability") on this repository, or email
`security@hydronode.tech`. We acknowledge reports within five business days and coordinate disclosure.

Include what you tested, what happened, and how to reproduce it. Never include real tokens or sensor
secrets; describing where they appear is enough.

## Scope

- The `hnmcp` server in this repository and the hosted endpoint at `https://hydronode.tech/mcp`.
- Issues in the HydroNode backend, OAuth 2.1 endpoints or web app can be reported the same way.

## Security guarantees

- Stateless Go server with no database credentials and no server-side secrets.
- Accepts only `hoa_` agent access tokens (`Authorization: Bearer hoa_…`). Personal API tokens
  (`hat_`), refresh tokens (`hrt_`) and JWTs are rejected immediately without calling the backend.
- Calls only `/api/agent/v1/*` on the internal backend network, validates path segments and never
  follows HTTP redirects.
- Enforces `Origin` allowlist on `/mcp`, 64 KiB request body limit, 20 s request timeout and
  per-token rate limiting.
- Logs only tool name, status, duration and a 12-character SHA-256 token hash; never tokens,
  arguments or responses.
- Runs as a non-root user in a distroless container with a read-only root filesystem.
