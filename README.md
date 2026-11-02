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
- Per-alias strategies are configured under `strategies` and can be changed in the Combo Router dashboard. `fallback` tries candidates in order; `round-robin` rotates the starting candidate and then follows the ordered fallback chain; `smart` asks a configured selector model to pick a candidate, then runs ordered fallback; `fusion` calls every panel candidate concurrently and asks a judge to synthesize successful answers.
- `round-robin` rotation is in-memory per plugin process and resets when the plugin is reconfigured or restarted. `sticky_limit` (default 1) controls requests served from each starting candidate before rotating.
- Smart selection uses `selector.model`, optionally pins `selector.provider`, and expects JSON with a candidate `choice` and `confidence` from 0 to 1. The selector receives a compact prompt with only the latest user request and candidate list, not the full conversation/tool history. The selection call has a 60-second timeout; invalid, low-confidence, timed-out, or failed choices fail open to ordered fallback. `confidence_threshold` defaults to 0.8. Smart selection has no session stickiness or automatic capability discovery; optional image routing uses only the explicitly configured vision pool.
- Fusion waits for all panel calls (each capped at 90 seconds; at most 20 panel models per alias) and asks the configured `judge` model or the first panel candidate to synthesize successful answers. Panels and judge receive the original tool schemas and conversation history. The judge selects or synthesizes structured tool calls when more work is needed, so tool-enabled agent loops remain in Fusion; the judge's actual tool-call or final-answer response is returned to the client. Panel responses are treated as untrusted data in the judge prompt. Panel failures are tolerated if at least one panel returns extractable text or a structured tool call; the judge still synthesizes the result when only one panel succeeds or only one panel is configured. Fusion uses more tokens and parallel upstream capacity. Panels run non-streaming and the final judge response streams for streaming requests.
- Optional per-alias image-triggered routing uses `vision_enabled: true` and `vision_candidates`. Requests with structured image payloads use that pool for the whole request; text-only requests continue using `model_chains`. This applies to fallback, round-robin, Smart, and Fusion. Smart's selector still receives only its compact text prompt and selects among the vision candidates. For Fusion image requests, the vision candidates become panels and `vision_judge` overrides the judge; when omitted, the first vision candidate is the judge. Vision routing is disabled by default. Configure candidates that support images using the request's protocol.

### Selector vs. Judge Model Selection Guidelines

| Capability / Requirement | Smart Selector (`selector.model`)                                                                             | Fusion Judge (`judge.model`)                                                                                |
| :----------------------- | :------------------------------------------------------------------------------------------------------------ | :---------------------------------------------------------------------------------------------------------- |
| **Context Window**       | Small (~1k–4k tokens). Receives only the latest user text + candidate list.                                   | Large (32k–128k+ tokens). Receives full history, tool schemas, and all panel outputs.                       |
| **Vision Support**       | **Not required** for the selector. With image routing enabled, selected candidates come from the vision pool. | **Required** for image requests. Image routing uses the vision judge, or the first vision panel by default. |
| **Tool Calling**         | **Not required**. Emits JSON `{"choice": "...", "confidence": 0.0-1.0}`.                                      | **Required** for tool-enabled tasks. The judge selects or synthesizes the final tool call.                  |
| **Latency / Size**       | **Fast & light** (e.g., Flash / Mini). Latency directly delays candidate routing.                             | **High reasoning** (e.g., Sonnet / Pro). Quality matters more than raw speed.                               |
| **Failure Impact**       | Low risk (fails open to configured ordered fallback).                                                         | High risk (if the judge fails, the entire fusion request fails).                                            |

- Retries are explicit, ordered, bounded by `max_attempts`, and limited to configured HTTP statuses (default 403/429/502/503/504).
- `403` is retryable by default alongside `429` and `5xx`; other 4xx client errors and arbitrary non-status errors do not trigger fallback. A 403 usually signals credentials or model access restrictions, so fallback only helps if another candidate can succeed with different valid access.
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
          # Smart selection:
          # type: smart
          # selector: { model: example-selector-model }
          # confidence_threshold: 0.8
          # Fusion judge (optional; defaults to first panel model):
          # type: fusion
          # judge: { model: example-judge-model }
          # Optional image-triggered whole-request routing, for any strategy:
          # vision_enabled: true
          # vision_candidates:
          #   - model: example-vision-model
          #   - provider: vision-provider
          #     model: another-vision-model
          # Fusion only; defaults to the first vision candidate:
          # vision_judge: { model: example-vision-judge }
      retry_statuses: [403, 429, 502, 503, 504]
      max_attempts: 2
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
