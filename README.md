# CPA Combo Router Plugin

Native CLIProxyAPI plugin that exposes explicitly configured combo model aliases and routes requests through CPA's host-managed model executor. Each alias can use ordered fallback, round-robin, smart selection, or fusion with a judge. It uses the same downstream protocol for every candidate; configure model IDs and optional provider pins from the target CPA instance's available catalog.

## Safety and behavior

- Built against the CPA `v8.0.45` SDK. Official release packages target macOS amd64/arm64, Linux amd64/arm64, and Windows amd64.
- `model_chains` keys are client-facing aliases; each candidate has a provider-native model ID. The plugin changes the top-level request `model` to that native model on each attempt. `provider` is optional: omit it to let CPA resolve the model's provider from its live catalog and credentials; set it only to explicitly pin a confirmed CPA provider ID.
- Configure same-protocol candidates unless a separately verified protocol adapter is added. Candidate model APIs/schemas may differ even when the protocol labels are accepted.
- CPA host callback errors preserve structured HTTP status where available. Fallback is restricted to configured statuses; streaming fallback must not retry after downstream bytes are emitted.

- Only configured aliases are intercepted; ordinary model IDs pass through untouched.
- The plugin registers a CPA dashboard menu page, **Combo Router**, with controls to create/rename/delete aliases, add/edit/reorder/remove ordered model candidates, enable/disable routing, and save settings. Candidate models and the Smart selector model can be searched from one shared catalog loaded through the aggregate `/v1/models` request, or entered directly as custom IDs. CPA requires a client API key for this endpoint, so the dashboard provides a separate inline field for it, distinct from the management key. The key is kept in tab-scoped `sessionStorage` so navigation/reloads in the same tab do not prompt again; it is removed when the tab session ends or the user clicks **Forget key**. Configuration reads and writes use CPA's authenticated `/v0/management/plugins/:id/config` endpoint and CPA's remembered dashboard management key if available; otherwise the page prompts for that management key for the page session. It never uses the management key for `/v1/models`.
- Saving uses CPA's in-process configuration reload. The installed binary registers the dashboard menu/resource route when the plugin is loaded.
- Enabled aliases are registered with CPA through `model.register` and appear in `/v1/models`.
- Candidate calls use CPA's host model callback. When `provider` is omitted, CPA resolves the model against its current provider catalog and credentials; an explicit `provider` pins execution when required.
- Per-alias strategies are configured under `strategies` and can be changed in the Combo Router dashboard. `fallback` tries candidates in order; `round-robin` rotates the starting candidate per user message and then follows the ordered fallback chain; `smart` asks a configured selector model to pick a candidate, then runs ordered fallback; `fusion` calls every panel candidate concurrently and asks a judge to synthesize successful answers.
- `round-robin` rotation is in-memory per plugin process and resets when the plugin is reconfigured or restarted. `sticky_limit` (default 1) controls user messages served from each starting candidate before rotating. Tool steps within one task stay on the model that answered, because switching mid-task loses provider prompt caches and can break signed history. Set `rotate_per_request: true` to rotate on every request instead, which spreads quota more evenly during long tasks.
- Smart selection uses `selector.model`, optionally pins `selector.provider`, and expects JSON with a candidate `choice` and `confidence` from 0 to 1. The selector receives a compact prompt with only the latest user request (capped at 8 KiB) and the candidate list. It does not receive the client's system prompt, tools, or reasoning settings, and any output limit in the request is reduced to 512 tokens. Optional `candidate_hints` (`model: what it is good at`, YAML only, not shown in the dashboard) give the selector more than model names to go on. The selection call has a 10-second timeout (`selector_timeout_seconds`, 1-120); invalid, low-confidence, timed-out, or failed choices fail open to ordered fallback. `confidence_threshold` defaults to 0.8. The selector runs once per user message: tool steps within a task stay on the model that answered without another selection. Smart selection has no automatic capability discovery; optional image routing uses only the explicitly configured vision pool.
- Task stickiness for Smart and round-robin is held in memory for up to 2 hours and 4096 tasks, keyed by a hash of the first and latest user message and the alias, and resets on reconfigure or restart. A task whose first model failed stays on the model that answered.
- Fusion calls every panel model concurrently (each capped at `panel_timeout_seconds`, default 180; at most 20 panel models per alias) and asks the configured `judge` model or the first panel candidate to synthesize successful answers. Panels and judge receive the original tool schemas and conversation history. The judge selects or synthesizes structured tool calls when more work is needed, so tool-enabled agent loops remain in Fusion; the judge's actual tool-call or final-answer response is returned to the client. Panels run non-streaming and the judge response streams for streaming requests.
- Fusion sends the judge anonymized, shuffled, JSON-encoded proposals so it cannot favor a model's name or speed, and panel text cannot fake a proposal boundary. Panel responses are treated as untrusted data. Total panel output sent to the judge is capped at about 128 KiB of text and 128 KiB of tool calls, shared across proposals; longer proposals are marked truncated and the judge writes complete content itself.
- Fusion removes reasoning that a client replays in chat history (`reasoning_content`, `reasoning_details`, `reasoning` on assistant messages) for every model except the judge, because that thinking came from the judge. Some upstream paths ignore this field while others count it, Gemini through CPA at about double the plain-text token count, so a conversation can fit one panel and overflow another. Set `keep_history_reasoning: true` if a panel provider requires reasoning to be sent back. `fusion_history_reasoning` in logs reports turn and byte counts only.
- Images returned by tools in chat-format requests are moved into a user message right after the tool results, for every strategy and model. Some CPA translators turn tool content into text, so a screenshot's base64 would otherwise be counted token by token: one 800x800 PNG in a tool result exceeded Gemini's 1M limit, against about 1,100 tokens as a user-message image. `tool_images_moved` in logs reports image and byte counts only. A text-only model that rejects images in user messages can now reject these requests, so use `vision_candidates` for image work.
- Fusion panel logs are redacted by default. To diagnose a panel that keeps failing, set `log_panel_errors: true` to add the upstream error text to `fusion_panel_result`, then turn it off, because that text can quote request content.
- If every panel fails, models answer directly with the original request and no judge: a configured judge first, then panels, skipping any panel that rejected this exact request (`fusion_last_resort` in logs). Timed-out panels stay eligible because the direct attempt streams. The client error `all fusion panel models failed (...)` appears only when that also fails or nothing is eligible, and it names each model's reason, such as `gemini: HTTP 400: ...` or `luna: timed out after 180s`. It keeps an HTTP status when all failures agree.
- Panel failures are tolerated if at least one panel returns extractable text or a structured tool call; the judge still synthesizes the result when only one panel succeeds or only one panel is configured. If every judge attempt fails, including with a non-retryable error such as a context overflow, the first successful panel in configured order answers instead (`fusion_degraded` in logs). For streaming requests that panel is re-requested without the judge prompt unless the lead's stream is already buffered. Once judge output has reached the client there is no fallback.
- Optional Fusion fast path (YAML only, not shown in the dashboard): set `read_only_tools` to exact tool names. The first panel candidate (the lead) then answers alone first. If every tool call it makes is on the list, its response is returned directly and that step costs one model call. Text answers, unlisted tools, and unnamed calls go to the other panels and the judge, with the lead's answer as one proposal, so those steps cost the same as before but take longer because the lead runs first. Requests without tools skip the lead step. List only tools that cannot change anything: `terminal` does not belong here. Fast-path steps are not reviewed by the judge. For Zed's documented tools: `read_file`, `grep`, `find_path`, `list_directory`, `diagnostics`, `fetch`, `search_web`. Tool names differ per client, so check yours.
- Optional Fusion quorum: with `quorum: N` (default 0, wait for all), once N panels have succeeded the remaining panels get `quorum_grace_seconds` (default 15) and are then dropped (`fusion_quorum_reached` in logs). A low quorum can drop your slowest, most thorough model, so raise the grace period if that matters. CPA has no cancel call for non-streaming model calls, so a dropped panel's upstream call keeps running until it finishes or the client request ends. A lead that exceeds the timeout stops being waited for immediately, but CPA can only close its upstream stream once the first byte has arrived.
- Fusion sends no output while panels run. For clients with idle timeouts, set CPA's `streaming.keepalive-seconds` and `streaming.send-keepalive-before-ft: true`. Reported usage covers only the call whose response is returned, not the whole panel.
- Fusion passes the full history to every panel. Providers that require signed history, such as Gemini thought signatures on tool calls, may reject turns produced by another model. Check for `fusion_panel_result outcome=error status_code=400` in logs when mixing providers.
- Optional per-alias image-triggered routing uses `vision_enabled: true` and `vision_candidates`. Requests with structured image payloads use that pool for the whole request; text-only requests continue using `model_chains`. This applies to fallback, round-robin, Smart, and Fusion. Smart's selector still receives only its compact text prompt and selects among the vision candidates. For Fusion image requests, the vision candidates become panels and `vision_judge` overrides the judge; when omitted, the first vision candidate is the judge. Vision routing is disabled by default. Configure candidates that support images using the request's protocol.

### Selector vs. Judge Model Selection Guidelines

| Capability / Requirement | Smart Selector (`selector.model`)                                                                             | Fusion Judge (`judge.model`)                                                                                |
| :----------------------- | :------------------------------------------------------------------------------------------------------------ | :---------------------------------------------------------------------------------------------------------- |
| **Context Window**       | Small (~1k–4k tokens). Receives only the latest user text + candidate list.                                   | Large (32k–128k+ tokens). Receives full history, tool schemas, and all panel outputs.                       |
| **Vision Support**       | **Not required** for the selector. With image routing enabled, selected candidates come from the vision pool. | **Required** for image requests. Image routing uses the vision judge, or the first vision panel by default. |
| **Tool Calling**         | **Not required**. Emits JSON `{"choice": "...", "confidence": 0.0-1.0}`.                                      | **Required** for tool-enabled tasks. The judge selects or synthesizes the final tool call.                  |
| **Latency / Size**       | **Fast & light** (e.g., Flash / Mini). Latency directly delays candidate routing.                             | **High reasoning** (e.g., Sonnet / Pro). Quality matters more than raw speed.                               |
| **Failure Impact**       | Low risk (fails open to configured ordered fallback).                                                         | Medium risk (if every judge attempt fails, one panel answers without synthesis).                            |

- Every candidate in the list is tried, and `max_attempts` (default 3, 1-20) is the number of tries each candidate gets, 1 second apart, before the next one starts. A list of 10 models can make up to 30 attempts before the request fails. CPA may also try several accounts within one attempt. Fusion panels run once each, because the other panels and the direct attempt already cover a failed panel; `max_attempts` applies to the judge and direct attempts. Retries are limited to configured HTTP statuses (default 403/429/500/502/503/504). CPA reports refused connections and empty upstream streams as 500, so 500 is retried by default.
- `403` is retryable by default alongside `429` and `5xx`; other 4xx client errors and arbitrary non-status errors do not trigger fallback. A 403 usually signals credentials or model access restrictions, so fallback only helps if another candidate can succeed with different valid access. The exception is a 400 or 413 recognized as a context overflow, which moves straight to the next candidate because the same model would reject it again but the next may have a bigger context window.
- `first_byte_timeout_seconds` (default 120, 0 disables, otherwise 5-1800) moves a silent streaming attempt on to the next candidate. It never cuts off a stream that has started, and the final attempt of the final candidate is never limited, so it cannot turn a slow answer into an error when nothing else is left. CPA can only close the abandoned upstream call once its first byte arrives, but its late output never reaches the client. `attempt_timeout_seconds` (default 0, off, otherwise 5-1800) does the same for non-streaming attempts, measured over the whole response. Silent attempts count as tries, so with the defaults one silent model can take about 6 minutes before the next starts. Raise the first-byte limit if your models think for minutes before their first output, and lower it or `max_attempts` for faster failover.
- For streaming, fallback is allowed only before a non-empty downstream chunk was emitted. After output starts, terminate with the original error.
- Token-count requests are unsupported. Stream fallback is covered before/after emitted data, but does not preserve candidate-specific response headers. Confirm protocol and request/response schema compatibility for every candidate pair.

- Fallback behavior is covered by deterministic executor tests and a local CPA/mock-upstream integration test. Streaming does not retry after output has started.

## Configuration example

```yaml
plugins:
  enabled: true
  dir: ./plugins
  configs:
    cpa-combo-router:
      enabled: true
      priority: 20
      model_chains:
        combo-example:
          - model: example-primary-model
          - model: example-fallback-model
          # Optional explicit pin:
          # - provider: antigravity
          #   model: gemini-flash-latest
      strategies:
        combo-example:
          type: round-robin # fallback | round-robin | smart | fusion
          sticky_limit: 1
          # rotate_per_request: true # rotate on every request, including tool steps
          # Smart selection:
          # type: smart
          # selector: { model: example-selector-model }
          # confidence_threshold: 0.8
          # selector_timeout_seconds: 10
          # candidate_hints: { example-primary-model: fast, good for small edits }
          # Fusion judge (optional; defaults to first panel model):
          # type: fusion
          # judge: { model: example-judge-model }
          # panel_timeout_seconds: 180 # 10-600
          # quorum: 2 # 0 waits for all panels
          # quorum_grace_seconds: 15 # 1-300
          # read_only_tools: [read_file, grep, find_path, list_directory] # exact names; empty disables the fast path
          # keep_history_reasoning: true # send replayed reasoning to every panel, not only the judge
          # Optional image-triggered whole-request routing, for any strategy:
          # vision_enabled: true
          # vision_candidates:
          #   - model: example-vision-model
          #   - provider: vision-provider
          #     model: another-vision-model
          # Fusion only; defaults to the first vision candidate:
          # vision_judge: { model: example-vision-judge }
      retry_statuses: [403, 429, 500, 502, 503, 504]
      # first_byte_timeout_seconds: 120 # streaming; 0 disables
      # attempt_timeout_seconds: 0 # non-streaming; 0 disables
      # log_panel_errors: false # adds upstream error text to fusion logs; may quote request content
      max_attempts: 3 # tries per model
```

The plugin config key must match `cpa-combo-router`. Prefer omitting `provider`: CPA then resolves the candidate model using the same catalog and credentials as a normal request. If you pin `provider`, use the CPA execution provider key, not the model catalog's `owned_by` label.

## Verification status

- `go test -race ./...`, `go vet ./...`, `git diff --check`, dashboard JavaScript syntax validation, and a local CGO shared-library build are run for strategy changes. Dashboard tests assert that the picker uses one aggregate `/v1/models` endpoint and contains no per-auth-file model requests.
- Executor RPC tests use fake host callbacks; a local CPA instance and mock upstream cover fallback behavior, including streaming retry rules.
- Release packages are built from source with Go `1.26.10` and contain a platform-native CPA plugin library.

## License

This project is licensed under the [MIT License](LICENSE).

## Build and tests

Requires CGO, a C compiler, and the Go toolchain specified by `go.mod` (Go 1.26.10+):

```sh
go test ./...
go build -buildmode=c-shared -o dist/cpa-combo-router.so .
```

This command builds a shared library for the current host. Tagged GitHub releases use `.github/workflows/release.yml` to test, build, and package native libraries for the five supported store platforms with SHA-256 checksums.
