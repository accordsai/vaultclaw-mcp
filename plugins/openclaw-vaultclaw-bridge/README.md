# Vaultclaw OpenClaw Bridge

This plugin exposes `vaultclaw_*` tools directly inside OpenClaw by spawning `accords-mcp` over stdio and forwarding MCP `tools/call`.

## Install

```bash
openclaw plugins install /Users/sam/code/accords-mcp/plugins/openclaw-vaultclaw-bridge
openclaw plugins enable vaultclaw-openclaw-bridge
```

## Minimal Config

Set plugin command to your local `accords-mcp` binary:

```bash
openclaw config set plugins.entries.vaultclaw-openclaw-bridge.config.command /Users/sam/code/accords-mcp/bin/accords-mcp
```

If you already store Vaultclaw token/socket in skill env (`skills.entries.vaultclaw*.env`), the plugin reads those automatically.

## Optional Overrides

- `plugins.entries.vaultclaw-openclaw-bridge.config.env`:
  explicit env map passed to `accords-mcp`.
- `plugins.entries.vaultclaw-openclaw-bridge.config.args`:
  additional command args.
- `plugins.entries.vaultclaw-openclaw-bridge.config.callTimeoutMs`:
  per-call timeout.
- `plugins.entries.vaultclaw-openclaw-bridge.config.startupTimeoutMs`:
  MCP init timeout.
