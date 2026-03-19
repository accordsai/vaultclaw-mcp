import { Type } from "@sinclair/typebox";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import type { OpenClawConfig, OpenClawPluginApi } from "openclaw/plugin-sdk";

type BridgePluginConfig = {
  enabled: boolean;
  command: string;
  args: string[];
  env: Record<string, string>;
  skillEnvOrder: string[];
  startupTimeoutMs: number;
  callTimeoutMs: number;
};

type ToolDescriptor = {
  name: string;
  description?: string;
  inputSchema?: unknown;
};

type Pending = {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  timer: NodeJS.Timeout;
};

const DEFAULTS: BridgePluginConfig = {
  enabled: true,
  command: "accords-mcp",
  args: [],
  env: {},
  skillEnvOrder: ["vaultclaw", "vaultclaw_google"],
  startupTimeoutMs: 15000,
  callTimeoutMs: 45000,
};

function readString(value: unknown): string | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

function readStringArray(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const out = value.map((item) => readString(item)).filter((item): item is string => Boolean(item));
  return out.length > 0 ? out : undefined;
}

function readNumber(value: unknown): number | undefined {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return undefined;
  }
  return Math.trunc(value);
}

function readStringRecord(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(value)) {
    const sv = readString(v);
    if (sv !== undefined) {
      out[k] = sv;
    }
  }
  return out;
}

function normalizePluginConfig(raw: unknown): BridgePluginConfig {
  const record = raw && typeof raw === "object" && !Array.isArray(raw)
    ? (raw as Record<string, unknown>)
    : {};
  const cfg: BridgePluginConfig = {
    enabled: typeof record.enabled === "boolean" ? record.enabled : DEFAULTS.enabled,
    command: readString(record.command) ?? DEFAULTS.command,
    args: readStringArray(record.args) ?? DEFAULTS.args,
    env: readStringRecord(record.env) ?? DEFAULTS.env,
    skillEnvOrder: readStringArray(record.skillEnvOrder) ?? DEFAULTS.skillEnvOrder,
    startupTimeoutMs: readNumber(record.startupTimeoutMs) ?? DEFAULTS.startupTimeoutMs,
    callTimeoutMs: readNumber(record.callTimeoutMs) ?? DEFAULTS.callTimeoutMs,
  };

  if (cfg.startupTimeoutMs < 1000 || cfg.startupTimeoutMs > 120000) {
    throw new Error("startupTimeoutMs must be within 1000..120000");
  }
  if (cfg.callTimeoutMs < 1000 || cfg.callTimeoutMs > 300000) {
    throw new Error("callTimeoutMs must be within 1000..300000");
  }
  return cfg;
}

function maybeResolvePath(api: OpenClawPluginApi, command: string): string {
  if (!command.includes("/") && !command.includes("\\") && !command.startsWith(".")) {
    return command;
  }
  return api.resolvePath(command);
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  return value as Record<string, unknown>;
}

function readSkillEnv(config: OpenClawConfig, skillName: string): Record<string, string> {
  const root = config as unknown as Record<string, unknown>;
  const skills = asRecord(root.skills);
  const entries = asRecord(skills?.entries);
  const entry = asRecord(entries?.[skillName]);
  const env = asRecord(entry?.env);
  if (!env) {
    return {};
  }
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(env)) {
    const sv = readString(v);
    if (sv !== undefined) {
      out[k] = sv;
    }
  }
  return out;
}

function resolveBridgeEnv(config: OpenClawConfig, pluginConfig: BridgePluginConfig): NodeJS.ProcessEnv {
  const merged: NodeJS.ProcessEnv = { ...process.env };
  for (const skillName of pluginConfig.skillEnvOrder) {
    Object.assign(merged, readSkillEnv(config, skillName));
  }
  Object.assign(merged, pluginConfig.env);
  return merged;
}

class NdjsonRpcClient {
  private nextId = 1;
  private readonly pending = new Map<number, Pending>();
  private readonly stderrLines: string[] = [];
  private exited = false;
  private readonly onLine: (line: string) => void;

  constructor(
    private readonly child: ReturnType<typeof spawn>,
    private readonly label: string,
    private readonly logger: OpenClawPluginApi["logger"],
  ) {
    this.onLine = (line: string) => {
      const trimmed = line.trim();
      if (!trimmed) {
        return;
      }
      let parsed: Record<string, unknown>;
      try {
        parsed = JSON.parse(trimmed) as Record<string, unknown>;
      } catch {
        this.logger.warn(`[${this.label}] invalid JSON line from MCP server`);
        return;
      }
      const id = parsed.id;
      if (typeof id !== "number") {
        return;
      }
      const pending = this.pending.get(id);
      if (!pending) {
        return;
      }
      this.pending.delete(id);
      clearTimeout(pending.timer);
      const errorObj = asRecord(parsed.error);
      if (errorObj) {
        const msg = readString(errorObj.message) ?? "MCP request failed";
        pending.reject(new Error(msg));
        return;
      }
      pending.resolve(parsed.result);
    };

    if (this.child.stdout) {
      const rl = createInterface({ input: this.child.stdout });
      rl.on("line", this.onLine);
    }

    if (this.child.stderr) {
      const errRl = createInterface({ input: this.child.stderr });
      errRl.on("line", (line) => {
        const t = line.trim();
        if (t.length > 0) {
          this.stderrLines.push(t);
        }
      });
    }

    this.child.on("exit", (code, signal) => {
      this.exited = true;
      const suffix = this.stderrLines.length > 0 ? ` stderr: ${this.stderrLines.join(" | ")}` : "";
      const error = new Error(
        `[${this.label}] MCP process exited (code=${String(code)}, signal=${String(signal)})${suffix}`,
      );
      for (const [id, pending] of this.pending) {
        this.pending.delete(id);
        clearTimeout(pending.timer);
        pending.reject(error);
      }
    });

    this.child.on("error", (error) => {
      this.exited = true;
      const suffix = this.stderrLines.length > 0 ? ` stderr: ${this.stderrLines.join(" | ")}` : "";
      const wrapped = new Error(
        `[${this.label}] failed to start MCP process: ${error.message}${suffix}`,
      );
      for (const [id, pending] of this.pending) {
        this.pending.delete(id);
        clearTimeout(pending.timer);
        pending.reject(wrapped);
      }
    });
  }

  request(method: string, params: unknown, timeoutMs: number): Promise<unknown> {
    if (this.exited) {
      return Promise.reject(new Error(`[${this.label}] MCP process already exited`));
    }
    const id = this.nextId++;
    const payload = {
      jsonrpc: "2.0",
      id,
      method,
      params,
    };
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`[${this.label}] MCP request timeout: ${method}`));
      }, timeoutMs);

      this.pending.set(id, { resolve, reject, timer });
      if (!this.child.stdin) {
        this.pending.delete(id);
        clearTimeout(timer);
        reject(new Error(`[${this.label}] MCP stdin is not available`));
        return;
      }
      this.child.stdin.write(`${JSON.stringify(payload)}\n`, (err) => {
        if (!err) {
          return;
        }
        this.pending.delete(id);
        clearTimeout(timer);
        reject(new Error(`[${this.label}] failed to write MCP request: ${String(err)}`));
      });
    });
  }

  notify(method: string, params: unknown) {
    if (this.exited || !this.child.stdin) {
      return;
    }
    const payload = {
      jsonrpc: "2.0",
      method,
      params,
    };
    this.child.stdin.write(`${JSON.stringify(payload)}\n`);
  }

  close() {
    if (this.exited) {
      return;
    }
    try {
      this.child.stdin?.end();
    } catch {
      // ignore
    }
    setTimeout(() => {
      if (!this.exited) {
        this.child.kill("SIGTERM");
      }
    }, 50);
  }
}

async function withMcpClient<T>(params: {
  api: OpenClawPluginApi;
  cfg: BridgePluginConfig;
  run: (client: NdjsonRpcClient) => Promise<T>;
}): Promise<T> {
  const command = maybeResolvePath(params.api, params.cfg.command);
  const child = spawn(command, params.cfg.args, {
    env: resolveBridgeEnv(params.api.config, params.cfg),
    stdio: ["pipe", "pipe", "pipe"],
  });

  const client = new NdjsonRpcClient(child, "vaultclaw-bridge", params.api.logger);

  try {
    await client.request(
      "initialize",
      {
        protocolVersion: "2024-11-05",
        capabilities: {},
        clientInfo: { name: "openclaw-vaultclaw-bridge", version: "0.1.0" },
      },
      params.cfg.startupTimeoutMs,
    );
    client.notify("notifications/initialized", {});
    return await params.run(client);
  } catch (error) {
    throw formatLaunchError(error, command);
  } finally {
    client.close();
  }
}

function normalizeToolSchema(inputSchema: unknown): Record<string, unknown> {
  const fallback = Type.Object({}, { additionalProperties: true }) as unknown as Record<string, unknown>;
  const schema = asRecord(inputSchema);
  if (!schema) {
    return fallback;
  }
  return sanitizeJsonSchema(schema, 0) ?? fallback;
}

function sanitizeJsonSchema(value: unknown, depth: number): Record<string, unknown> | undefined {
  if (depth > 20) {
    return undefined;
  }
  const schema = asRecord(value);
  if (!schema) {
    return undefined;
  }

  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(schema)) {
    out[k] = v;
  }

  const type = readString(out.type);
  if (type === "array") {
    const items = sanitizeJsonSchema(out.items, depth + 1);
    out.items = items ?? {};
  }

  const properties = asRecord(out.properties);
  if (properties) {
    const next: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(properties)) {
      next[k] = sanitizeJsonSchema(v, depth + 1) ?? (Type.Object({}, { additionalProperties: true }) as unknown as Record<string, unknown>);
    }
    out.properties = next;
  }

  if (Array.isArray(out.oneOf)) {
    out.oneOf = out.oneOf
      .map((item) => sanitizeJsonSchema(item, depth + 1))
      .filter((item): item is Record<string, unknown> => Boolean(item));
  }
  if (Array.isArray(out.anyOf)) {
    out.anyOf = out.anyOf
      .map((item) => sanitizeJsonSchema(item, depth + 1))
      .filter((item): item is Record<string, unknown> => Boolean(item));
  }
  if (Array.isArray(out.allOf)) {
    out.allOf = out.allOf
      .map((item) => sanitizeJsonSchema(item, depth + 1))
      .filter((item): item is Record<string, unknown> => Boolean(item));
  }

  const additionalProperties = out.additionalProperties;
  if (additionalProperties && typeof additionalProperties === "object" && !Array.isArray(additionalProperties)) {
    out.additionalProperties = sanitizeJsonSchema(additionalProperties, depth + 1) ?? true;
  }

  if (!readString(out.type) && !out.oneOf && !out.anyOf && !out.allOf) {
    out.type = "object";
    if (!("additionalProperties" in out)) {
      out.additionalProperties = true;
    }
  }

  return out;
}

function normalizeContentItems(rawContent: unknown): Array<{ type: "text"; text: string }> {
  if (!Array.isArray(rawContent)) {
    return [];
  }
  const out: Array<{ type: "text"; text: string }> = [];
  for (const item of rawContent) {
    const record = asRecord(item);
    if (!record) {
      continue;
    }
    const type = readString(record.type);
    const text = readString(record.text);
    if (type === "text" && text !== undefined) {
      out.push({ type: "text", text });
    }
  }
  return out;
}

function parseJsonRecord(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const trimmed = value.trim();
  if (!trimmed) {
    return undefined;
  }
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    return asRecord(parsed);
  } catch {
    return undefined;
  }
}

function extractMcpEnvelope(record: Record<string, unknown>): Record<string, unknown> | undefined {
  const direct = asRecord(record);
  if (direct && typeof direct.ok === "boolean") {
    return direct;
  }

  const structured = asRecord(record.structuredContent);
  if (structured && typeof structured.ok === "boolean") {
    return structured;
  }

  const nestedResult = asRecord(record.result);
  if (nestedResult && typeof nestedResult.ok === "boolean") {
    return nestedResult;
  }

  const content = normalizeContentItems(record.content);
  for (const item of content) {
    const parsed = parseJsonRecord(item.text);
    if (!parsed) {
      continue;
    }
    if (typeof parsed.ok === "boolean") {
      return parsed;
    }
    const parsedResult = asRecord(parsed.result);
    if (parsedResult && typeof parsedResult.ok === "boolean") {
      return parsedResult;
    }
  }

  return undefined;
}

function buildApprovalHint(envelope: Record<string, unknown>): string | undefined {
  if (envelope.ok !== false) {
    return undefined;
  }
  const error = asRecord(envelope.error);
  const code = readString(error?.code);
  if (code !== "MCP_APPROVAL_REQUIRED") {
    return undefined;
  }
  const details = asRecord(error?.details);
  const approval = asRecord(details?.approval);
  const pendingApproval = asRecord(approval?.pending_approval);
  const challengeId = readString(approval?.challenge_id);
  const pendingId = readString(approval?.pending_id);
  const runId = readString(approval?.run_id);
  const jobId = readString(approval?.job_id);
  const markdownLink =
    readString(approval?.remote_attestation_link_markdown) ??
    readString(pendingApproval?.remote_attestation_link_markdown);
  const url =
    readString(approval?.remote_attestation_url) ??
    readString(pendingApproval?.remote_attestation_url) ??
    (markdownLink && markdownLink.startsWith("http") ? markdownLink : undefined);

  const parts: string[] = ["Approval required in Vaultclaw UI."];
  if (challengeId) {
    parts.push(`challenge_id: ${challengeId}`);
  }
  if (pendingId) {
    parts.push(`pending_id: ${pendingId}`);
  }
  if (runId) {
    parts.push(`run_id: ${runId}`);
  }
  if (jobId) {
    parts.push(`job_id: ${jobId}`);
  }
  if (markdownLink) {
    parts.push(`Attestation link: ${markdownLink}`);
  } else if (url) {
    parts.push(`Attestation link: ${url}`);
  }
  return parts.join(" ");
}

function formatLaunchError(error: unknown, command: string): Error {
  const text = error instanceof Error ? error.message : String(error);
  return new Error(`failed to launch accords-mcp command "${command}": ${text}`);
}

function toToolResult(rawResult: unknown, toolName: string) {
  const record = asRecord(rawResult) ?? {};
  const content = normalizeContentItems(record.content);
  const envelope = extractMcpEnvelope(record);
  const structuredContent = envelope ?? record.structuredContent;
  const approvalHint = envelope ? buildApprovalHint(envelope) : undefined;
  const compatResult =
    envelope && asRecord(envelope.result)
      ? asRecord(envelope.result)
      : envelope && asRecord(envelope.data)
        ? asRecord(envelope.data)
        : envelope;
  const finalContent =
    content.length > 0
      ? content
      : [
        {
          type: "text" as const,
          text: JSON.stringify(structuredContent ?? record, null, 2),
        },
      ];
  if (approvalHint && !finalContent.some((item) => item.text.includes("Approval required in Vaultclaw UI."))) {
    finalContent.unshift({ type: "text", text: approvalHint });
  }
  const explicitIsError = typeof record.isError === "boolean" ? record.isError : undefined;
  const derivedIsError = envelope?.ok === false;
  return {
    ok: envelope?.ok === true,
    result: compatResult,
    data: envelope?.data,
    error: envelope?.error,
    meta: envelope?.meta,
    isError: explicitIsError ?? derivedIsError,
    structuredContent,
    content: finalContent,
    details: {
      bridge: "vaultclaw-openclaw-bridge",
      backend: "accords-mcp-stdio",
      tool: toolName,
      structuredContent,
      mcpEnvelope: envelope,
    },
  };
}

function toToolLabel(name: string): string {
  return name.replace(/^vaultclaw_/, "").replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

const plugin = {
  id: "vaultclaw-openclaw-bridge",
  name: "Vaultclaw OpenClaw Bridge",
  description: "Expose vaultclaw_* tools directly in OpenClaw by bridging to accords-mcp over stdio MCP.",
  version: "0.1.0",
  configSchema: {
    parse(value: unknown) {
      return normalizePluginConfig(value);
    },
    uiHints: {
      enabled: { label: "Enable Bridge" },
      command: { label: "accords-mcp Command" },
      args: { label: "Command Args" },
      env: { label: "Environment Overrides" },
      skillEnvOrder: { label: "Skill Env Sources" },
      startupTimeoutMs: { label: "Startup Timeout (ms)" },
      callTimeoutMs: { label: "Tool Call Timeout (ms)" },
    },
  },
  async register(api: OpenClawPluginApi) {
    const cfg = normalizePluginConfig(api.pluginConfig);
    if (!cfg.enabled) {
      api.logger.info("[vaultclaw-bridge] disabled by config");
      return;
    }

    let tools: ToolDescriptor[] = [];
    try {
      const listed = await withMcpClient({
        api,
        cfg,
        run: async (client) => {
          const result = await client.request("tools/list", {}, cfg.callTimeoutMs);
          const record = asRecord(result);
          const rawTools = Array.isArray(record?.tools) ? record.tools : [];
          return rawTools;
        },
      });
      tools = listed
        .map((item) => {
          const record = asRecord(item);
          if (!record) {
            return null;
          }
          const name = readString(record.name);
          if (!name) {
            return null;
          }
          return {
            name,
            description: readString(record.description),
            inputSchema: record.inputSchema,
          } satisfies ToolDescriptor;
        })
        .filter((item): item is ToolDescriptor => Boolean(item))
        .filter((item) => item.name.startsWith("vaultclaw_"));
    } catch (error) {
      api.logger.warn(
        `[vaultclaw-bridge] failed to discover tools: ${error instanceof Error ? error.message : String(error)}`,
      );
      return;
    }

    for (const tool of tools) {
      api.registerTool(
        {
          name: tool.name,
          label: toToolLabel(tool.name),
          description: tool.description ?? `Vaultclaw tool ${tool.name}`,
          parameters: normalizeToolSchema(tool.inputSchema),
          async execute(_toolCallId: string, params: Record<string, unknown>) {
            const result = await withMcpClient({
              api,
              cfg,
              run: async (client) =>
                await client.request(
                  "tools/call",
                  {
                    name: tool.name,
                    arguments: params ?? {},
                  },
                  cfg.callTimeoutMs,
                ),
            });
            return toToolResult(result, tool.name);
          },
        },
        { name: tool.name },
      );
    }

    api.logger.info(`[vaultclaw-bridge] registered ${tools.length} Vaultclaw tools`);
  },
};

export default plugin;
