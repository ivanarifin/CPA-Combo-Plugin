# CPA Combo Router Plugin

Native CLIProxyAPI plugin that exposes explicitly configured combo model aliases and runs an ordered model fallback chain through CPA's host-managed model executor. It uses the same downstream protocol for every candidate; configure model IDs and optional provider pins from the target CPA instance's available catalog.

## Safety and behavior

- Built against the CPA `v8.0.45` SDK. Official release packages target macOS amd64/arm64, Linux amd64/arm64, and Windows amd64.
- `model_chains` keys are client-facing aliases; each candidate has a provider-native model ID. The plugin changes the top-level request `model` to that native model on each attempt. `provider` is optional: omit it to let CPA resolve the model's provider from its live catalog and credentials; set it only to explicitly pin a confirmed CPA provider ID.
- Configure same-protocol candidates unless a separately verified protocol adapter is added. Candidate model APIs/schemas may differ even when the protocol labels are accepted.
- CPA host callback errors preserve structured HTTP status where available. Fallback is restricted to configured statuses; streaming fallback must not retry after downstream bytes are emitted.

- Only configured aliases are intercepted; ordinary model IDs pass through untouched.
- The plugin registers a CPA dashboard menu page, **Combo Router**, with controls to create/rename/delete aliases, add/edit/reorder/remove ordered model candidates, enable/disable routing, and save settings. Candidate models can be selected from one aggregate `/v1/models` request or entered as a custom ID. CPA requires a client API key for this endpoint, so the dashboard provides a separate inline field for it, distinct from the management key. The key is kept in tab-scoped `sessionStorage` so navigation/reloads in the same tab do not prompt again; it is removed when the tab session ends or the user clicks **Forget key**. Configuration reads and writes use CPA's authenticated `/v0/management/plugins/:id/config` endpoint and CPA's remembered dashboard management key if available; otherwise the page prompts for that management key for the page session. It never uses the management key for `/v1/models`.
- Saving uses CPA's in-process configuration reload. The installed binary registers the dashboard menu/resource route when the plugin is loaded.
- Enabled aliases are registered with CPA through `model.register` and appear in `/v1/models`.
- Candidate calls use CPA's host model callback. When `provider` is omitted, CPA resolves the model against its current provider catalog and credentials; an explicit `provider` pins execution when required.
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
      retry_statuses: [403, 429, 502, 503, 504]
      max_attempts: 2
```

The plugin config key must match `cpa-combo-router`. Prefer omitting `provider`: CPA then resolves the candidate model using the same catalog and credentials as a normal request. If you pin `provider`, use the CPA execution provider key, not the model catalog's `owned_by` label.

## Verification status

- `go test -race -cover ./...`, `go vet ./...`, `git diff --check`, and JavaScript syntax validation pass. Dashboard tests assert that the picker uses one aggregate `/v1/models` endpoint and contains no per-auth-file model requests.
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
