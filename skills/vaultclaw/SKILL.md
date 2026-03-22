---
name: vaultclaw
description: Route natural-language requests to Vaultclaw cookbooks and execute bounded/unbounded connector flows with strict validation and approval-safe behavior.
metadata: |
  {
    "openclaw": {
      "primaryEnv": "VC_AGENT_TOKEN",
      "requires": {
        "env": ["VC_AGENT_TOKEN"]
      }
    }
  }
---

For Vaultclaw requests, use direct `vaultclaw_*` tools exposed by OpenClaw plugin id `vaultclaw-openclaw-bridge`. `mcporter` compatibility mode is deprecated and is not part of the supported runtime contract.

## Runtime Standard (OpenClaw 2026.3.x)

- Required runtime path: direct `vaultclaw_*` tool calls through plugin id `vaultclaw-openclaw-bridge`.
- Required companion plugin: `vaultclaw-mcp-approval-handoff` for approval wait and auto-resume.
- Required auth env: `VC_AGENT_TOKEN`.
- Do not attempt alternate execution paths when direct tools are unavailable.

### Deterministic Setup Error Contract

If `vaultclaw_session_status` is unavailable or direct `vaultclaw_*` tools are missing, return this setup error and stop:

1. `openclaw plugins install /Users/sam/code/accords-mcp/plugins/openclaw-vaultclaw-bridge`
2. `openclaw plugins enable vaultclaw-openclaw-bridge`
3. `openclaw config set plugins.entries.vaultclaw-openclaw-bridge.config.command /Users/sam/code/accords-mcp/bin/accords-mcp`
4. `openclaw plugins install @vaultclaw/vaultclaw-mcp-approval-handoff`
5. `openclaw plugins enable vaultclaw-mcp-approval-handoff`
6. Restart gateway/session and retry.

Do not fallback to `mcporter`.

## Phase-1 Routing Scope

- Domain enabled now: `google.gmail` via cookbook `google.workspace@1.3.0`.
- Routing data sources:
  - `references/routes.google_gmail.v1.json`
  - `references/slots.google_gmail.v1.json`
  - `references/document_type_aliases.google_gmail.v1.json`
- Do not use free-form cookbook search as primary routing.

## Hard Rules

1. Never use `gog`.
2. Never use browser workflows for email tasks.
3. On each run, verify direct tool path by calling `vaultclaw_session_status`.
4. Configure Vaultclaw session on each run via direct tools:
   - `vaultclaw_session_configure({"base_url":"http://localhost","token":"$VC_AGENT_TOKEN","timeout_ms":20000})`
5. Resolve route by `intent + domain` from `routes.google_gmail.v1.json` first.
6. Verify selected entry with `vaultclaw_recipe_get` before rendering/executing (`recipe_id` must be the route `entry_id` value).
7. For templates, always render first with `vaultclaw_template_render`.
8. Validate before execute:
   - `vaultclaw_connector_validate` for `VERB_REQUEST`
   - `vaultclaw_plan_validate` for `PLAN`
9. Execute only after validation succeeds.
10. If approval is required (including attestation flows), ask user to approve in Vaultclaw UI, then immediately call `vaultclaw_approval_wait` using the returned handle.
11. For approval-required flows, do not stop after surfacing approval details; wait until `vaultclaw_approval_wait` returns terminal status or timeout.
12. If route confidence is low, ask exactly one clarification question, then continue.
13. If still unmapped after one clarification, ask user to choose:
   - proceed with direct connector call, or
   - fail safely and stop.
14. If `vaultclaw_route_resolve` returns multiple `missing_input_guidance` items with `resolution_mode=AUTO_RETRY_WITH_FACTS` and `external_fact_request.parallelizable=true`, resolve all requested facts in parallel, then retry route resolution once with all facts merged into `context.facts`.

## Canonical Pipeline

1. Configure session.
2. Classify intent/domain and extract slots.
3. Resolve deterministic route from registry.
4. For document-bearing email intents, resolve `document_attachments` by type.
5. `vaultclaw_recipe_get(cookbook_id, recipe_id, version)`.
6. If `entry_type` is template, render with extracted `inputs`.
7. Validate rendered payload.
8. Preflight document attachment resolution before execute.
9. Execute.
10. If approval required, call `vaultclaw_approval_wait(handle)` and wait for terminal status or timeout.
11. Return concise summary with `cookbook_id`, `entry_id`, `version`, and applied inputs.

Parallel fact enrichment contract:

1. If route resolve returns multiple `AUTO_RETRY_WITH_FACTS` items, treat them as a batch.
2. Resolve all fact requests concurrently when `external_fact_request.parallelizable=true`.
3. Retry `vaultclaw_route_resolve` once with merged facts (for example `weather_summary`, `email_subject`, `email_body`).
4. Only ask the user if unresolved required inputs remain after that retry.
5. For `generic.http` missing inputs, preserve the emitted `fact_key` names directly (for example `url`, `api_key`) in `context.facts` on retry.

## Approval Wait Contract

When an execute call returns approval-required (`MCP_APPROVAL_REQUIRED`):

1. Always surface `challenge_id` and `pending_id`.
2. Include attestation link when present:
   - prefer `remote_attestation_link_markdown`,
   - otherwise render `remote_attestation_url` as clickable Markdown.
3. Immediately call:
   - `vaultclaw_approval_wait(handle, timeout_ms=600000, poll_interval_ms=1500)`
4. Do not send a final completion message before wait returns:
   - terminal outcome (`SUCCEEDED`, `DENIED`, `FAILED`, or equivalent), or
   - timeout (`MCP_WAIT_TIMEOUT`).
5. Always send one final user update after wait returns:
   - success: execution completed,
   - failure/denied: execution did not complete,
   - timeout: still pending and can be resumed by calling `vaultclaw_approval_wait` again with the same handle.

## Intent Routing (Google Gmail)

Use the exact route IDs in `references/routes.google_gmail.v1.json`:

- `send_email` -> `gmail_tpl_plan_send_email_v1`
- `reply_email` -> `gmail_tpl_plan_reply_in_thread_v1`
- `create_draft` -> `gmail_tpl_plan_create_draft_v1`
- `trash_message` -> `gmail_tpl_plan_trash_message_v1`
- `untrash_message` -> `gmail_tpl_plan_untrash_message_v1`
- `get_message_metadata` -> `gmail_tpl_verb_messages_get_metadata_v1`
- `get_message_content` -> `gmail_tpl_verb_messages_get_content_v1`
- `list_messages_by_label` -> `gmail_tpl_verb_messages_list_by_label_v1`
- `modify_message_labels` -> `gmail_tpl_verb_messages_modify_labels_v1`
- `list_labels` -> `gmail_recipe_labels_list_v1`
- `list_inbox` -> `gmail_recipe_messages_list_inbox_v1`
- `send_status_update` -> `gmail_recipe_plan_send_status_update_v1`

For `vaultclaw_recipe_get`, pass route `entry_id` as `recipe_id`.

## Slot and Payload Rules

Use `references/slots.google_gmail.v1.json` as source of truth for required slots and normalization.

Key payload constraints:

- Recipient fields are arrays: `to`, `cc`, `bcc`
- Body fields: `text_plain`, `text_html`
- Document attachments: `document_attachments[]` with object keys:
  - `type_id` (required)
  - `filename` (optional)
  - `content_type` (optional)
  - no additional keys allowed
- Do not use legacy `body_text`
- Use explicit `version=1.3.0` for `google.workspace` in route resolution

For attachment-only prompts missing subject/body, use deterministic defaults:

- `subject`: `Document: <DisplayName>` (fallback `Requested document`)
- `text_plain`: `Attached is my <display_name>.` (fallback `Attached is the requested document.`)

## Document Attachment Resolution

When intent is `send_email`, `reply_email`, or `create_draft` and prompt implies attachment:

1. Build candidate document phrase from user text.
2. Call `vaultclaw_document_types_suggest(query, top_k=5)`.
3. Accept top suggestion when confidence passes:
   - top `score >= 40`, OR
   - top reasons include `type_token_match` or `display_name_match` and second score is not within 5.
4. If no confident suggestion, fallback to `references/document_type_aliases.google_gmail.v1.json`.
5. If still unresolved, ask one clarification question (max 1).
6. Build strict payload:
   - `document_attachments: [{ "type_id": "<resolved_type_id>" }]`
   - include `filename` / `content_type` only when explicitly provided.

## Fallback Search Policy (Secondary Only)

Use `vaultclaw_recipes_search` only when:

- deterministic route lookup fails, or
- mapped entry is missing from catalog.

Fallback search constraints:

- include `entry_type`
- include tags from the route registry
- prefer newest cookbook version
- if multiple viable candidates remain, ask one clarification question

## Direct Connector Fallback (User-Approved Only)

If user explicitly chooses direct connector call:

1. Fetch connector metadata via `vaultclaw_connector_get`.
2. Read `policy.verbs[verb].input_schema_v1`.
3. Build request using schema-allowed fields only.
4. Validate with `vaultclaw_connector_validate`.
5. If validation fails, return field errors and stop.

## Document Preflight

Before executing any Gmail payload with `document_attachments`:

1. For each attachment call `vaultclaw_document_types_latest(type_id, subject_id=self)`.
2. If unresolved/not found:
   - do not execute,
   - ask one guided question to confirm type and instruct upload/assign,
   - then retry once.
3. If still unresolved after one clarification, fail safely with clear reason.

## Validation and Smoke Checklist

Before declaring routing ready:

1. Every route entry resolves with `vaultclaw_recipe_get`.
2. Template routes render with minimal valid inputs.
3. Plan/verb validation passes for rendered payloads.
4. At least one approval-required flow reaches `vaultclaw_approval_wait` terminal state.
5. No covered Gmail prompt returns "No matching Vaultclaw recipe/template".
