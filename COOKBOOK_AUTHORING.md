# COOKBOOK_AUTHORING.md

Authoritative v1 standard for creating cookbook bundles in this repository.

## Purpose

This document defines how to author:

- cookbook bundles (`accords.cookbook.bundle.v1`)
- recipes (`recipe.verb.v1`, `recipe.plan.v1`)
- templates (`template.verb.v1`, `template.plan.v1`)
- executable plans derived from recipes/templates

Goals:

- keep authored assets valid against current MCP/catalog behavior
- make generation repeatable for humans and Codex
- require test coverage for authoring changes

Non-goals:

- introducing new runtime APIs or schema fields
- changing MCP server execution behavior
- adding CI gates in this phase

## Audience

- engineers authoring cookbook assets
- agents generating cookbook assets
- reviewers validating cookbook PRs

## Normative Sources

The rules here are derived from runtime behavior and tests in this repo:

- `internal/catalog/types.go`
- `internal/catalog/validate.go`
- `internal/catalog/render.go`
- `internal/catalog/store.go`
- `internal/mcp/module_hash.go`
- `internal/orchestration/plan_preflight.go`
- `internal/orchestration/unbounded_profiles.go`
- `internal/catalog/*_test.go`
- `internal/orchestration/*_test.go`
- `internal/mcp/tools_catalog_test.go`
- `internal/mcp/integration_smoke_test.go`

When this file and code diverge, code behavior is the source of truth. Update this file in the same PR when behavior changes.

## Asset Hierarchy

1. `Bundle` (cookbook): versioned container of entries.
2. `Entry`: one recipe or template.
3. `Rendered payload`: a `VERB_REQUEST` or `PLAN`.
4. `Execution payload`: request to `vaultclaw_connector_execute*` or `vaultclaw_plan_*`.

## Decision Tree: Recipe vs Plan vs Template

Use this in order:

1. Is the content fully concrete now (no runtime input mapping)?
- Yes: use recipe.
- No: use template.

2. Is it one connector action (`connector_id` + `verb` + `request`)?
- Yes: use `recipe.verb.v1` or `template.verb.v1`.
- No: use `recipe.plan.v1` or `template.plan.v1`.

3. Is this a one-off execution that should not be cataloged?
- Yes: build a direct plan payload and execute; do not create a cookbook entry.
- No: store as a recipe/template entry in a bundle.

## Connector Track Model

Author every entry using one of two tracks.

| Track | Use When | Core Requirements | Common Failure Modes |
| --- | --- | --- | --- |
| Bounded connector | Connector has bounded verbs/policies (example: `google.gmail.*`) | Stable `connector_id` + `verb`; request shape fits connector policy; module hash semantics respected | policy drift/module hash requirements, approval-required execution paths |
| `generic.http` | External HTTP target with secret attachment semantics | `secret_attachments` modeled correctly; plan preflight can resolve `plan_input` refs; unbounded profile behavior considered | unresolved preflight refs, missing attachment fields, missing compatible profile |

## Bundle Canonical Structure

Use this baseline structure.

```json
{
  "type": "accords.cookbook.bundle.v1",
  "cookbook_id": "example.cookbook",
  "version": "1.0.0",
  "title": "Example Cookbook",
  "description": "Optional",
  "tags": ["example"],
  "entries": []
}
```

## Rule / Why / Fails With

### Bundle-Level Rules

| Rule | Why | Fails With |
| --- | --- | --- |
| `bundle.type` must be `accords.cookbook.bundle.v1` | Catalog only validates this type | `MCP_CATALOG_SCHEMA_INVALID` |
| `cookbook_id`, `version`, `title` are required and non-empty | Required identity and metadata for storage/index | `MCP_CATALOG_SCHEMA_INVALID` |
| `entries` must be non-empty | Empty bundles are invalid by schema | `MCP_CATALOG_SCHEMA_INVALID` |
| `entry_id` must be unique within a bundle | Index/search/addressability requirement | `MCP_CATALOG_SCHEMA_INVALID` |
| `cookbook_id` and `version` must be safe identifiers | Prevent path traversal and invalid storage keys | `MCP_CATALOG_SCHEMA_INVALID` |
| Conflict policy defaults to `FAIL` | Prevent silent overwrite on content drift | `MCP_CATALOG_CONFLICT` on mismatch |

### Entry-Type Rules

| Entry Type | Rule | Why | Fails With |
| --- | --- | --- | --- |
| `recipe.verb.v1` | requires `connector_id`, `verb`, `request` object | executable request recipe | `MCP_CATALOG_SCHEMA_INVALID` |
| `recipe.plan.v1` | requires `plan` object | executable plan recipe | `MCP_CATALOG_SCHEMA_INVALID` |
| `template.verb.v1` | requires `base_request` object | renderable request template | `MCP_CATALOG_SCHEMA_INVALID` |
| `template.plan.v1` | requires `base_plan` object | renderable plan template | `MCP_CATALOG_SCHEMA_INVALID` |
| template bindings | `target_path` valid JSON pointer, `input_key` required, no duplicate `target_path+input_key` | deterministic rendering and missing-input reporting | `MCP_CATALOG_SCHEMA_INVALID` |
| unknown `entry_type` | unsupported | schema hard boundary | `MCP_CATALOG_SCHEMA_INVALID` |

### Template Rendering Rules

| Rule | Why | Fails With |
| --- | --- | --- |
| `template_id` must reference a template entry type | render endpoint is template-only | `MCP_TEMPLATE_NOT_FOUND` |
| `output_kind=AUTO` resolves by entry type | prevents ambiguous output shape | n/a |
| `template.verb.v1` only supports `VERB_REQUEST` or `AUTO` | shape safety | `MCP_TEMPLATE_INPUT_INVALID` |
| `template.plan.v1` only supports `PLAN` or `AUTO` | shape safety | `MCP_TEMPLATE_INPUT_INVALID` |
| missing required bindings return unresolved error | explicit input contract | `MCP_TEMPLATE_RENDER_UNRESOLVED` |
| rendered `VERB_REQUEST` must include `connector_id`, `verb`, `request` object | executable request shape | `MCP_TEMPLATE_INPUT_INVALID` |
| rendered `PLAN` must include non-empty `steps` | executable plan shape | `MCP_TEMPLATE_INPUT_INVALID` |

## Entry Authoring Standards

### `recipe.verb.v1`

Use when:

- one concrete connector action should be reusable as-is

Minimum fields:

- `entry_id`
- `entry_type=recipe.verb.v1`
- `connector_id`
- `verb`
- `request`

Recommended fields:

- `title`, `description`, `tags`, `policy_version`

Anti-patterns:

- using a recipe when inputs vary heavily per invocation
- embedding pseudo-templates in `request` instead of using bindings

### `recipe.plan.v1`

Use when:

- a fixed multi-step flow should be reused with little or no parameterization

Minimum fields:

- `entry_id`
- `entry_type=recipe.plan.v1`
- `plan` with non-empty `steps`

Recommended fields:

- `plan_input_defaults` when plan has optional runtime inputs
- descriptive `title`, `tags`

Anti-patterns:

- using a plan recipe for highly variable runs that should be templated

### `template.verb.v1`

Use when:

- one request shape is stable but key values vary by run

Minimum fields:

- `entry_id`
- `entry_type=template.verb.v1`
- `base_request`
- `bindings`

Binding conventions:

- `target_path` must be valid JSON pointer path
- prefer explicit `required` on every binding (`true`/`false`)
- use `default` only for safe, low-risk defaults

### `template.plan.v1`

Use when:

- multi-step flow structure is stable but step values vary by run

Minimum fields:

- `entry_id`
- `entry_type=template.plan.v1`
- `base_plan`
- `bindings`

Binding conventions:

- bind leaf values only, not structural nodes
- use stable pointer paths that survive plan evolution
- avoid pointers that require implicit array growth unless intentional

## Bounded Connector Authoring Standard

This track covers connectors that are not `generic.http` unbounded-profile flows.

### Required Practices

1. Always set explicit `connector_id` and canonical `verb` in recipes/templates.
2. Keep request shape policy-compliant for the target connector.
3. Prefer templates over recipes when values vary materially per execution.
4. Design for approval-required outcomes on execution flows.

### Module Hash Rules

Runtime applies module hash derivation logic:

- `google.gmail.*` verbs on connector `google` force module hash derivation from connector `policy_hash` when absent.
- Legacy secret-id fields (`token_secret_id`, `required_secret_ids`) force module hash derivation.
- If policy hash cannot be derived, execution/validate can fail with `MCP_MODULE_HASH_REQUIRED`.

Authoring guidance:

1. Do not hardcode `module_hash` in cookbook assets unless pinning is intentional.
2. Assume module hash can be injected by runtime on bounded flows.
3. If pinning `module_hash`, document update policy for drift.

### Approval-Aware Plan Notes

For plans that may trigger approvals:

1. Expect possible `MCP_APPROVAL_REQUIRED`.
2. Ensure smoke flows cover wait/resume behavior and terminal deny behavior.

## `generic.http` Authoring Standard

### Required Practices

1. Use `connector_id=generic.http`, `verb=generic.http.request.v1`.
2. Model secrets via `secret_attachments` for unbounded profile matching:
- `slot` required
- `mode` required
- `target` required
- optional: `intent`, `expected_secret_types`, `required`
3. Prefer deterministic targets and modes to improve profile match stability.
4. For plan bindings, use `plan_input` refs only when the input is guaranteed at runtime.

### Preflight and Binding Behavior

Plan preflight only orchestrates `generic.http.request.v1` steps.

Implications:

1. Mixed plans should keep non-HTTP steps independent from profile orchestration.
2. Unresolved `plan_input` refs in strict preflight fail with:
- `MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED`
- `required_plan_input_paths` in error details
3. If `profile_id` is already present, it is respected.
4. If no `secret_attachments` exist, no profile resolution is needed for that step.

### Auto-Create Profile Behavior

When no compatible profile exists:

1. if `auto_create_profiles=false`, expect `MCP_UNBOUNDED_PROFILE_REQUIRED`
2. if `auto_create_profiles=true`, runtime attempts deterministic profile creation
3. missing scope for create path returns `MCP_UNBOUNDED_PROFILE_REQUIRED` with scope message

Authoring guidance:

1. For production cookbooks, prefer explicit profile strategy documentation.
2. For tests, include both existing-profile and auto-create/error paths when feasible.

## Decision Matrix: Bounded vs `generic.http`

| Question | Prefer Bounded | Prefer `generic.http` |
| --- | --- | --- |
| Native connector verb exists for use case | Yes | No |
| Need policy-governed bounded behavior | Yes | Optional |
| Need custom arbitrary HTTP targeting | No | Yes |
| Need unbounded profile secret attachment model | No | Yes |
| Need low-variance, connector-native workflow | Yes | Sometimes |

## Testing Contract (Mandatory)

Every cookbook authoring PR must include unit and smoke coverage evidence.

### Unit Test Requirements

Required scenario matrix:

1. Schema validation:
- valid bundle for each used entry type
- invalid/unsupported entry type
- missing required fields per used entry type
- duplicate `entry_id`

2. Template rendering:
- required input missing returns unresolved error
- default value application works as expected
- output kind mismatch fails
- rendered plan has non-empty `steps`

3. `generic.http` preflight (when relevant):
- static request resolves profile path
- unresolved `plan_input` produces precheck unresolved error
- mixed plan only orchestrates `generic.http` steps
- auto-create profile path and failure path documented/tested where applicable

### Smoke Test Requirements

Required scenario matrix:

1. Plan validate and execute path exercised.
2. Approval-required path exercised.
3. Wait/resume terminal deny behavior exercised.
4. Catalog round trip exercised (`upsert/get/search/render` at minimum for changed asset types).

### Commands

Unit:

```bash
go test ./...
```

Integration smoke:

```bash
VC_BASE_URL=http://127.0.0.1:8787 \
VC_AGENT_TOKEN=<token> \
go test -tags=integration ./internal/mcp -run '^TestSmoke_(Session|Discovery|Execute|ExecuteJob|Plan|Approvals)' -v
```

Manual deny-resume smoke:

```bash
VC_BASE_URL=http://127.0.0.1:8787 \
VC_AGENT_TOKEN=<token> \
VC_SMOKE_MANUAL_APPROVAL=1 \
VC_SMOKE_EXPECT_OUTCOME=DENY \
go test -tags=integration ./internal/mcp -run '^TestSmoke_ManualApprovalResume_Deny$' -v
```

## PR Checklist (Copy/Paste)

Use this block in PR descriptions for cookbook authoring changes:

```markdown
### Cookbook Authoring Checklist
- [ ] Bundle uses `accords.cookbook.bundle.v1` and valid identifiers.
- [ ] Entry types are correct (`recipe.verb.v1` / `recipe.plan.v1` / `template.verb.v1` / `template.plan.v1`).
- [ ] Recipe vs template choice documented.
- [ ] Bounded vs `generic.http` track choice documented.
- [ ] Template bindings validated (`target_path`, `input_key`, duplicates, required/default intent).
- [ ] Unit test scenarios covered (schema + render + preflight where relevant).
- [ ] Smoke scenarios covered (validate/execute + approval + wait + catalog round trip).
- [ ] Prompt(s) used for generation included or linked.
- [ ] No MCP runtime API/schema changes introduced.
```

## Prompt Templates for Codex

### 1) Full Cookbook Generation Prompt

```text
Create a full cookbook bundle for <DOMAIN> following /Users/sam/code/accords-mcp/COOKBOOK_AUTHORING.md.

Constraints:
- Output bundle type must be accords.cookbook.bundle.v1.
- Use only supported entry types.
- Choose bounded connector track or generic.http track explicitly per entry.
- Include at least:
  - 2 recipe.verb.v1 entries
  - 1 recipe.plan.v1 entry
  - 1 template.verb.v1 entry
  - 1 template.plan.v1 entry
- Add titles/descriptions/tags.
- Keep IDs/version safe for storage.

Then generate:
1) unit tests for schema/render behavior
2) smoke test plan and commands for execution behavior

Do not change runtime APIs. Explain recipe vs template decisions for each entry.
```

### 2) Recipe Generation Prompt

```text
Generate a cookbook entry of type <recipe.verb.v1|recipe.plan.v1> for <USE_CASE>.
Follow /Users/sam/code/accords-mcp/COOKBOOK_AUTHORING.md exactly.

Return:
1) entry JSON
2) why recipe (not template)
3) unit test cases needed for this entry
4) smoke scenarios impacted
```

### 3) Plan Template Generation Prompt

```text
Generate a template.plan.v1 entry for <USE_CASE> following /Users/sam/code/accords-mcp/COOKBOOK_AUTHORING.md.

Requirements:
- Provide base_plan with non-empty steps.
- Provide bindings with valid JSON pointer target_path.
- Mark required/default behavior explicitly.
- Provide example inputs payload.
- Show expected rendered PLAN shape.

Also provide unit tests for required-missing/default/output-kind behavior.
```

### 4) Unit Test Generation Prompt

```text
Given this cookbook bundle diff, generate/extend Go unit tests to satisfy the mandatory matrix in /Users/sam/code/accords-mcp/COOKBOOK_AUTHORING.md.

Must include:
- schema validity and invalidity cases
- duplicate entry_id case
- template render missing/default/mismatch/shape cases
- generic.http preflight cases if generic.http entries exist
```

### 5) Smoke Test Generation Prompt

```text
Given this cookbook bundle diff, generate a smoke validation plan aligned with /Users/sam/code/accords-mcp/COOKBOOK_AUTHORING.md.

Must include:
- validate + execute flow
- approval-required path
- wait/resume deny path
- catalog round trip checks

Return exact commands and required env vars.
```

### 6) Invalid Bundle Repair Prompt

```text
Repair this invalid cookbook bundle to conform to /Users/sam/code/accords-mcp/COOKBOOK_AUTHORING.md and current repo validation/render rules.

Return:
1) corrected JSON
2) list of violations fixed
3) mapping of each fix to expected error code that would have occurred
4) tests to prevent regression
```

## Adoption and Migration

1. No forced rewrite of existing cookbook assets.
2. Apply this standard whenever a bundle is touched.
3. If an existing bundle violates this standard but behavior is intentional, document the exception in the PR.

