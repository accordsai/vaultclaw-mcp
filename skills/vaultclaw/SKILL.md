---
name: vaultclaw
description: Route natural-language requests to Vaultclaw cookbooks and execute bounded/unbounded connector flows with strict validation and approval-safe behavior.
metadata: |
  {
    "openclaw": {
      "primaryEnv": "VC_AGENT_TOKEN",
      "requires": {
        "env": ["VC_AGENT_TOKEN", "MCPORTER_CONFIG"]
      }
    }
  }
---

For Vaultclaw requests, use `mcporter` + `accords-vaultclaw` MCP only.

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
3. Configure Vaultclaw session on each run:
   `mcporter call accords-vaultclaw.vaultclaw_session_configure --args "{\"base_url\":\"http://localhost\",\"token\":\"$VC_AGENT_TOKEN\",\"timeout_ms\":20000}" --output json`
4. Resolve route by `intent + domain` from `routes.google_gmail.v1.json` first.
5. Verify selected entry with `vaultclaw_recipe_get` before rendering/executing.
6. For templates, always render first with `vaultclaw_template_render`.
7. Validate before execute:
   - `vaultclaw_connector_validate` for `VERB_REQUEST`
   - `vaultclaw_plan_validate` for `PLAN`
8. Execute only after validation succeeds.
9. If approval is required, ask user to approve in Vaultclaw UI, then call `vaultclaw_approval_wait`.
10. If route confidence is low, ask exactly one clarification question, then continue.
11. If still unmapped after one clarification, ask user to choose:
   - proceed with direct connector call, or
   - fail safely and stop.

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
10. If approval required, resume with `vaultclaw_approval_wait`.
11. Return concise summary with `cookbook_id`, `entry_id`, `version`, and applied inputs.

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
