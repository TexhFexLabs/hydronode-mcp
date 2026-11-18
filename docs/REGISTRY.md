# Listing in the MCP Registry

`server.json` describes the hosted server for <https://registry.modelcontextprotocol.io>. Published as
`tech.hydronode/hydronode` 0.1.0 on 2026-11-18.

Before publishing, check the current `$schema` URL and field names in the registry docs; they change between
schema versions.

## Namespace

- `tech.hydronode/hydronode` (chosen): proves ownership of `hydronode.tech` via a DNS TXT record.
  `mcp-publisher login dns --domain hydronode.tech --private-key <ed25519 key>` prints the TXT value; set it in
  Cloudflare once, keep the private key offline.
- Alternative `io.github.texhfexlabs/hydronode`: `mcp-publisher login github` by an org member. No DNS change.

## Publish

The Ed25519 key lives in the maintainer's macOS login keychain (service `hydronode-mcp-registry-ed25519`,
account `hydronode.tech`); the TXT record on `hydronode.tech` holds its public half.

```bash
mcp-publisher validate
mcp-publisher login dns -domain hydronode.tech \
  -private-key "$(security find-generic-password -s hydronode-mcp-registry-ed25519 -a hydronode.tech -w)"
mcp-publisher publish
```

Bump `version` in `server.json` for every listing change. Clients and directories (VS Code, Smithery, Glama,
PulseMCP) read the registry. Claude's Connectors Directory has its own submission: `docs/CLAUDE_DIRECTORY.md`.
