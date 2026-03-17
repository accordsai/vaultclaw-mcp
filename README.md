# accords-mcp (Plans 1 + 2 + 3)

Go `stdio` MCP server for Vaultclaw actions plus local cookbook/template catalog rendering and remote pull install.

## What This Implements

- `stdio`-only MCP server.
- Token-only session configuration (no passphrase handling).
- Connector tools: list/get/validate/execute/execute-job.
- Document helper tools: suggest/latest for Vaultclaw document types.
- Plan tools: validate/execute/run status.
- Prerequisite tools: slot bindings + unbounded profiles.
- Automatic unbounded profile orchestration for:
  - `vaultclaw_connector_execute_job`
  - `vaultclaw_plan_execute`
- Explicit orchestration helpers:
  - `vaultclaw_unbounded_profile_resolve`
  - `vaultclaw_plan_unbounded_profile_preview`
- Deterministic error normalization to MCP-friendly envelope.
- Automatic `module_hash` derivation from connector `policy_hash` for bounded execution paths.
- External approval handoff for `execute-job` and `plan-execute`.
- Explicit wait/resume polling tool for post-approval continuation.
- Read-only approval visibility tools (pending list/get + job/run status outcome projection).
- Local cookbook bundle CRUD/import/export catalog.
- Recipe/template discovery tools and template render tools.
- Remote catalog source registry + pull-only list/install with optional SHA256 verification.
- Strict Plan 3 boundary: catalog/render tools never execute Vault actions.

## Non-Goals

- Approval signing or decision submission.
- Vault init/unlock/lock.
- Remote cookbook publish/post APIs.
- HTTP MCP transport.

## Build and Run

```bash
go run ./cmd/accords-mcp
```

This starts an MCP server over stdin/stdout.

## Cookbook Authoring Standard

For cookbook/recipe/template/plan authoring conventions and required test coverage, see:

- [COOKBOOK_AUTHORING.md](COOKBOOK_AUTHORING.md)

## MCP Envelope

Every tool call returns:

```json
{
  "ok": true,
  "data": {},
  "error": null,
  "meta": {
    "request_id": "...",
    "vault_http_status": 200,
    "vault_code": ""
  }
}
```

On failures:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "MCP_*",
    "category": "auth|approval|validation|policy|secrets|plans|network|internal",
    "message": "...",
    "retryable": false,
    "vault_code": "...",
    "details": {}
  },
  "meta": {
    "request_id": "...",
    "vault_http_status": 4xx,
    "vault_code": "..."
  }
}
```

## Tools

### Session

1. `vaultclaw_session_configure(base_url, token, timeout_ms?)`
2. `vaultclaw_session_status()`
3. `vaultclaw_session_clear()`

Example:

```json
{
  "name": "vaultclaw_session_configure",
  "arguments": {
    "base_url": "http://127.0.0.1:8080",
    "token": "vc_tok_...",
    "timeout_ms": 20000
  }
}
```

### Core Actions

1. `vaultclaw_connectors_list()`
2. `vaultclaw_connector_get(connector_id)`
3. `vaultclaw_connector_validate(request)`
4. `vaultclaw_connector_execute(request)`
5. `vaultclaw_connector_execute_job(request, orchestration?)`
6. `vaultclaw_document_types_suggest(query, top_k?)`
7. `vaultclaw_document_types_latest(type_id, subject_id?)`

`vaultclaw_connector_validate` calls Vaultclaw `/v0/connectors/validate` and returns field-level validation errors (`path`, `code`, `message`, `expected`, `actual`) when payload shape is invalid.

`vaultclaw_connector_execute` and `vaultclaw_connector_execute_job` also run module-hash preflight:

- Google Gmail bounded verbs: always derive `module_hash` from connector `policy_hash` unless already provided.
- Generic HTTP legacy secret-id bindings (`token_secret_id` / `required_secret_ids` paths): derive `module_hash`.
- Generic HTTP unbounded profile/slot-intent path without legacy secret-id bindings: module hash is not forced.

`vaultclaw_connector_execute` and `vaultclaw_connector_execute_job` always validate via `/v0/connectors/validate` before execution. If validation fails, they return early and do not request/trigger approval execution paths.

`vaultclaw_connector_execute_job` orchestration defaults:

```json
{
  "unbounded_profiles": true,
  "auto_create_profiles": true
}
```

Document helper tool behavior:

- `vaultclaw_document_types_suggest` calls Vaultclaw `/v0/docs/types/suggest`.
- `vaultclaw_document_types_latest` calls Vaultclaw `/v0/docs/types/latest`.
- For non-success responses, MCP preserves Vaultclaw code/details in the failure envelope (`vault_code` and `details.vault_error`).

Example:

```json
{
  "name": "vaultclaw_connector_execute_job",
  "arguments": {
    "request": {
      "connector_id": "generic.http",
      "verb": "generic.http.request.v1",
      "request": {
        "method": "GET",
        "url": "https://api.example.com/data",
        "secret_attachments": [
          {
            "slot": "api_key",
            "intent": "http.auth",
            "expected_secret_types": ["api-key"],
            "mode": "read",
            "target": "https://api.example.com"
          }
        ]
      }
    }
  }
}
```

### Safe Gmail Execution Order

1. Discover (`vaultclaw_recipes_search`) and prefer newest cookbook entries first (`google.workspace@1.3.0` before `1.2.0`).
2. Render template (`vaultclaw_template_render`) or load recipe/plan payload.
3. Validate payload (`vaultclaw_connector_validate` or `vaultclaw_plan_validate`).
4. If payload includes `document_attachments`, preflight each `type_id` with `vaultclaw_document_types_latest`.
5. Execute only after validation/preflight passes.
6. If approval is required, wait for user approval, then resume via `vaultclaw_approval_wait`.

Canonical Gmail payload shape:

- Recipients must be arrays: `to`, `cc`, `bcc`
- Message bodies use `text_plain` / `text_html`
- Document attachments use `document_attachments` array with strict object keys:
  - required: `type_id`
  - optional: `filename`, `content_type`
  - no additional keys

Deprecated Gmail shapes (rejected by schema validation):

- `body_text`
- Scalar recipient fields (for example, `to: "user@example.com"` instead of `to: ["user@example.com"]`)

Example attachment-aware prompt flow:

1. Prompt: `send my passport over email to a@b.com`
2. Resolve type via `vaultclaw_document_types_suggest(query="passport", top_k=5)` -> `identity.passport`.
3. Build render inputs with:
   - `to=["a@b.com"]`
   - `subject="Document: Passport"` (default if missing)
   - `text_plain="Attached is my passport."` (default if missing)
   - `document_attachments=[{"type_id":"identity.passport"}]`
4. Render+validate Gmail plan and preflight with `vaultclaw_document_types_latest(type_id="identity.passport", subject_id="self")`.
5. Execute only if preflight resolves.

### Plans

1. `vaultclaw_plan_validate(plan)`
2. `vaultclaw_plan_execute(plan, plan_input?, orchestration?)`
3. `vaultclaw_plan_run_get(run_id)`

`vaultclaw_plan_validate` and `vaultclaw_plan_execute` do not inject `module_hash` into plan step `request_base` payloads.

`vaultclaw_plan_execute` orchestration defaults:

```json
{
  "unbounded_profiles": true,
  "auto_create_profiles": true
}
```

### Approval Visibility + Resume

1. `vaultclaw_job_get(job_id)`
2. `vaultclaw_approvals_pending_list(states?, limit?, connector_id?, verb?, agent_id?, challenge_id?)`
3. `vaultclaw_approvals_pending_get(challenge_id, pending_id)`
4. `vaultclaw_approval_wait(handle, timeout_ms?, poll_interval_ms?)`

Wait defaults and clamps:

- default `timeout_ms=600000`, min `1000`, max `3600000`
- default `poll_interval_ms=1500`, min `250`, max `10000`

Example:

```json
{
  "name": "vaultclaw_plan_execute",
  "arguments": {
    "plan": {
      "steps": [
        {
          "step_id": "s1",
          "connector_id": "generic.http",
          "verb": "generic.http.request.v1",
          "request_base": {
            "method": "GET",
            "url": "https://api.example.com/data",
            "secret_attachments": [
              {
                "slot": "api_key",
                "mode": "read",
                "target": "https://api.example.com"
              }
            ]
          }
        }
      ]
    },
    "plan_input": {}
  }
}
```

### Prerequisite Helpers

1. `vaultclaw_slot_bindings_list(connector_id, verb, include_revoked?)`
2. `vaultclaw_slot_bind(connector_id, verb, slot, secret_id)`
3. `vaultclaw_unbounded_profiles_list(connector_id?, verb?, include_revoked?)`
4. `vaultclaw_unbounded_profile_get(profile_id)`
5. `vaultclaw_unbounded_profile_upsert(profile)`

### Orchestration/Preview

1. `vaultclaw_unbounded_profile_resolve(requirements, auto_create=true)`
2. `vaultclaw_plan_unbounded_profile_preview(plan, plan_input?)`

### Catalog + Templates (Plan 3)

Catalog CRUD:

1. `vaultclaw_cookbooks_list(filter?)`
2. `vaultclaw_cookbook_get(cookbook_id, version?)`
3. `vaultclaw_cookbook_upsert(bundle, conflict_policy?)`
4. `vaultclaw_cookbook_delete(cookbook_id, version?)`
5. `vaultclaw_cookbook_export(cookbook_id, version?)`
6. `vaultclaw_cookbook_import(bundle, conflict_policy?)`

Recipe/template access:

1. `vaultclaw_recipes_search(query?, connector_id?, verb?, tags?, entry_type?)`
2. `vaultclaw_recipe_get(cookbook_id, recipe_id, version?)`

Template rendering (no execution):

1. `vaultclaw_template_render(cookbook_id, template_id, version?, inputs, output_kind?)`
2. `output_kind`: `AUTO | VERB_REQUEST | PLAN`

Remote pull:

1. `vaultclaw_catalog_sources_list()`
2. `vaultclaw_catalog_source_upsert(source)`
3. `vaultclaw_catalog_source_delete(source_id)`
4. `vaultclaw_cookbooks_remote_list(source_id, query?)`
5. `vaultclaw_cookbook_remote_install(source_id, cookbook_id, version?, conflict_policy?, expected_sha256?)`

Search ordering note:

- `vaultclaw_recipes_search` returns higher cookbook versions first when multiple versions contain matching entries.

Example template render:

```json
{
  "name": "vaultclaw_template_render",
  "arguments": {
    "cookbook_id": "http.recipes",
    "template_id": "tpl_get_user",
    "inputs": {
      "url": "https://api.example.com/users/42",
      "method": "GET"
    },
    "output_kind": "AUTO"
  }
}
```

Render response shape:

```json
{
  "ok": true,
  "data": {
    "rendered": {},
    "missing_inputs": [],
    "used_defaults": [],
    "source_ref": {
      "cookbook_id": "http.recipes",
      "version": "1.0.0",
      "template_id": "tpl_get_user",
      "entry_type": "template.verb.v1",
      "output_kind": "VERB_REQUEST"
    }
  }
}
```

Use the rendered payload with existing action tools (`vaultclaw_connector_execute`, `vaultclaw_connector_execute_job`, `vaultclaw_plan_validate`, `vaultclaw_plan_execute`) when you actually want execution.

## Vaultclaw Skill (Routing)

Portable repo skill source of truth:

- `skills/vaultclaw/SKILL.md`

Local runtime skill path used by OpenClaw:

- `~/.openclaw/workspace/skills/vaultclaw/SKILL.md`

Sync command:

```bash
mkdir -p ~/.openclaw/workspace/skills/vaultclaw/references
cp -f skills/vaultclaw/SKILL.md ~/.openclaw/workspace/skills/vaultclaw/SKILL.md
cp -f skills/vaultclaw/references/routes.google_gmail.v1.json ~/.openclaw/workspace/skills/vaultclaw/references/routes.google_gmail.v1.json
cp -f skills/vaultclaw/references/slots.google_gmail.v1.json ~/.openclaw/workspace/skills/vaultclaw/references/slots.google_gmail.v1.json
cp -f skills/vaultclaw/references/document_type_aliases.google_gmail.v1.json ~/.openclaw/workspace/skills/vaultclaw/references/document_type_aliases.google_gmail.v1.json
```

## Catalog Storage and Remote Defaults

- Catalog root default: `os.UserConfigDir()/accords-mcp/catalog`
- Override root with env var: `ACCORDS_MCP_CATALOG_DIR`
- Files:
  - `sources.json`
  - `index.json`
  - `cookbooks/<cookbook_id>/<version>.json`

Conflict policies:

- `FAIL` (default)
- `OVERWRITE`
- `SKIP_IF_EXISTS`

Remote source auth modes:

- `NONE`
- `BEARER_ENV` (token loaded from `auth_env_var`)

Remote integrity:

- `vaultclaw_cookbook_remote_install` verifies SHA256 when:
  - `expected_sha256` is provided by caller, or
  - `sha256` is present in remote index item.
- If no checksum is provided anywhere, install still proceeds (trusted-source model for this phase).

## Automatic Unbounded Profile Orchestration

### Execute Job

When request is `connector_id=generic.http` and `verb=generic.http.request.v1`:

1. Derive requirement set from `request.secret_attachments`.
2. List/get unbounded profiles.
3. Deterministically match by slot/intent/type/mode/target.
4. Pick winner by highest score, then lexical `profile_id`.
5. If none matched:
   - Auto-create minimal constrained profile if permitted.
   - Else return `MCP_UNBOUNDED_PROFILE_REQUIRED` with requirements.
6. Inject `profile_id` and call `/v0/connectors/execute-job`.

### Plan Execute

For each `generic.http.request.v1` step:

1. Resolve request shape from `request_base` + `request_bindings` using `plan_input`.
2. If unresolved runtime bindings are needed, fail strict preflight with:
   - `MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED`
   - `step_id`
   - unresolved refs
   - required `plan_input` paths
3. Resolve/create profile using same matching logic.
4. Inject `profile_id` into transformed plan.
5. Submit transformed plan to `/v0/connectors/plans/execute`.

## Approval Handoff and Resume (Plan 2)

`vaultclaw_connector_execute_job` and `vaultclaw_plan_execute` return immediate controlled handoff when approval is required:

- `ok=false`
- `error.code=MCP_APPROVAL_REQUIRED`
- `error.details.approval` includes:
  - `status=PENDING_APPROVAL`
  - `kind=JOB|PLAN_RUN`
  - `job_id`, `run_id`, `challenge_id`, `pending_id`
  - `pending_id_resolved` (best effort)
  - `pending_expires_at_unix_ms`
  - `pending_approval` raw payload
  - `decision_outcome=PENDING`
  - `next_action` pointing to `vaultclaw_approval_wait`

Example `next_action` payload:

```json
{
  "tool": "vaultclaw_approval_wait",
  "arguments": {
    "handle": {
      "kind": "PLAN_RUN",
      "run_id": "run_123",
      "job_id": "run_123",
      "challenge_id": "ach_123",
      "pending_id": "apj_123"
    }
  }
}
```

Resume flow:

1. Call execute tool (`execute-job` or `plan-execute`).
2. If `MCP_APPROVAL_REQUIRED`, surface pending to human.
3. Human approves externally (Vault/UI/other client).
4. Call `vaultclaw_approval_wait(handle)` until terminal:
   - `SUCCEEDED` -> `decision_outcome=ALLOW`
   - `DENIED` with approval denial codes -> `decision_outcome=DENY`
   - otherwise -> `decision_outcome=UNKNOWN`

## Determinism and Idempotency

- Auto-created `profile_id`: `mcp.auto.<12-char-sha256>` from normalized requirements.
- Auto-create upsert uses stable idempotency key:
  - `mcp-unbounded-profile-<same hash>`
- Requirement and slot arrays are sorted/deduped for deterministic output.
- Module-hash preflight uses connector `policy_hash` lookup with per-request in-memory caching.

## Error Normalization

Primary mappings:

- `CONNECTOR_APPROVAL_DECISION_REQUIRED`, `PLAN_APPROVAL_REQUIRED`, `CONNECTOR_APPROVAL_GRANT_REQUIRED` -> `MCP_APPROVAL_REQUIRED`
- `UNBOUNDED_PROFILE_SLOT_UNRESOLVED`, `UNBOUNDED_PROFILE_SLOT_VIOLATION`, `UNBOUNDED_PROFILE_NOT_FOUND`, `UNBOUNDED_PROFILE_REQUIRED_FIELDS_MISSING` -> `MCP_UNBOUNDED_PROFILE_INVALID`
- auto-create upsert `INSUFFICIENT_SCOPE` -> `MCP_UNBOUNDED_PROFILE_REQUIRED` (orchestration layer)
- unresolved plan precheck -> `MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED`
- wait timeout -> `MCP_WAIT_TIMEOUT`
- pending get miss -> `MCP_APPROVAL_PENDING_NOT_FOUND`
- missing derivable policy hash for required module-hash path -> `MCP_MODULE_HASH_REQUIRED`
- auth/validation/policy/secrets/plans/network/internal retained per mapping table in code.

## Tests

Run:

```bash
go test ./...
```

Included unit coverage:

- profile matcher exact/mismatch/tie-break behavior
- deterministic minimal profile builder and stable hash/profile id
- auto-create deterministic idempotency key + missing-scope handling
- plan preflight resolved/unresolved/mixed-step behavior
- vault error normalization mappings
- approval handoff extraction for execute-job and plan-execute
- pending-id best-effort resolver deterministic tie-break behavior
- wait/resume timeout + terminal outcome behavior
- pending get not-found + malformed-input behavior
- module-hash auto-derivation rules and handler injection behavior
- catalog bundle validation and entry-type schema checks
- local catalog upsert/list/get/delete and conflict-policy behavior
- template render binding resolution (required/default/array-path set semantics)
- remote index fetch/install and checksum verification behavior

## Integration Smoke (Real Vaultclaw)

This suite validates MCP behavior against a live Vaultclaw instance.

Assumptions:

- Vaultclaw is already running and unlocked.
- You provide a valid pre-minted agent token.
- `generic.http` connector is installed.

Environment contract:

- `VC_BASE_URL` (default: `http://127.0.0.1:8787`)
- `VC_UNIX_SOCKET` (optional; when set, MCP uses this Unix domain socket for local Vaultclaw HTTP transport)
- `VC_AGENT_TOKEN` (required)
- `VC_SMOKE_MANUAL_APPROVAL` (default: `0`; set `1` for manual resume test)
- `VC_SMOKE_WAIT_TIMEOUT_MS` (default: `600000`)
- `VC_SMOKE_POLL_INTERVAL_MS` (default: `1500`)
- `VC_SMOKE_EXPECT_OUTCOME` (default and required in this plan: `DENY`)

Required token scopes for full smoke coverage:

- `connectors.list.v1`
- `connectors.get.v1`
- `connectors.execute.v1`
- `connectors.execute_job.v1`
- `connectors.plans.execute.v1`
- `connectors.plans.read.v1`
- `connectors.approvals.pending.read.v1`

Automated core smoke:

```bash
VC_BASE_URL=http://127.0.0.1:8787 \
VC_AGENT_TOKEN=<token> \
go test -tags=integration ./internal/mcp -run '^TestSmoke_(Session|Discovery|Execute|ExecuteJob|Plan|Approvals)' -v
```

Automated core smoke over local Unix socket:

```bash
VC_UNIX_SOCKET="$HOME/Library/Application Support/vaultclaw/vaultd.sock" \
VC_BASE_URL=http://localhost \
VC_AGENT_TOKEN=<token> \
go test -tags=integration ./internal/mcp -run '^TestSmoke_(Session|Discovery|Execute|ExecuteJob|Plan|Approvals)' -v
```

Manual external-approval resume smoke (DENY):

```bash
VC_BASE_URL=http://127.0.0.1:8787 \
VC_AGENT_TOKEN=<token> \
VC_SMOKE_MANUAL_APPROVAL=1 \
VC_SMOKE_EXPECT_OUTCOME=DENY \
go test -tags=integration ./internal/mcp -run '^TestSmoke_ManualApprovalResume_Deny$' -v
```

Manual flow behavior:

1. The test submits plan execution and receives `MCP_APPROVAL_REQUIRED`.
2. It logs `challenge_id`, `pending_id`, and `run_id`.
3. Deny that pending approval externally (Vault UI/API).
4. Test calls `vaultclaw_approval_wait` and expects terminal `DENIED` with `decision_outcome=DENY`.
