package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
 if (stored_host == NULL || stored_host->call == NULL) return 1;
 return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}
static void free_host_buffer(void* ptr, size_t len) {
 if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) stored_host->free_buffer(ptr, len);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"unsafe"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const pluginID = "cpa-combo-router"
const executorProviderID = "cpa-combo-router-executor"

var currentConfig atomic.Value

type comboConfig struct {
	Enabled       bool                        `yaml:"enabled"`
	ModelChains   map[string][]comboCandidate `yaml:"model_chains"`
	RetryStatuses []int                       `yaml:"retry_statuses"`
	MaxAttempts   int                         `yaml:"max_attempts"`
}

type comboCandidate struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Protocol string `yaml:"protocol"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ModelRegistrar        bool     `json:"model_registrar"`
	ModelRouter           bool     `json:"model_router"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
	ManagementAPI         bool     `json:"management_api"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}
type rpcModelRouteRequest struct {
	pluginapi.ModelRouteRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}
type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}
type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}
type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}
type rpcStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}
type rpcStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

// StatusError preserves the HTTP status from the host's structured error envelope.
type StatusError struct {
	Code    int
	Message string
}

func (e StatusError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("upstream status %d", e.Code)
}

func (e StatusError) StatusCode() int { return e.Code }

func decodeStatusError(err error) error {
	if err == nil {
		return nil
	}
	var wire envelopeError
	if json.Unmarshal([]byte(err.Error()), &wire) == nil && wire.HTTPStatus > 0 {
		return StatusError{Code: wire.HTTPStatus, Message: wire.Message}
	}
	return err
}

func AsStatusError(err error) (StatusError, bool) {
	var status StatusError
	if errors.As(err, &status) {
		return status, status.Code > 0
	}
	var providerStatus interface{ StatusCode() int }
	if errors.As(err, &providerStatus) && providerStatus.StatusCode() > 0 {
		return StatusError{Code: providerStatus.StatusCode(), Message: err.Error()}, true
	}
	return StatusError{}, false
}

type hostModelCall func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error)
type candidateStreamCall func(context.Context, comboCandidate, func([]byte) error) error
type rpcHostCall func(string, any) (json.RawMessage, error)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var raw []byte
	if request != nil && requestLen > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	out, err := handleMethod(C.GoString(method), raw)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, out)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := configure(raw); err != nil {
			return nil, err
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodModelRegister:
		return registerModels()
	case pluginabi.MethodModelRoute:
		return routeModel(raw)
	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(map[string]string{"identifier": executorProviderID})
	case pluginabi.MethodExecutorExecute:
		return execute(raw)
	case pluginabi.MethodExecutorExecuteStream:
		return executeStream(raw)
	case pluginabi.MethodExecutorCountTokens:
		return errorEnvelope("unsupported", "combo plugin does not implement token counting"), nil
	case pluginabi.MethodManagementRegister:
		return registerManagement()
	case pluginabi.MethodManagementHandle:
		return handleManagement(raw)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	cfg := comboConfig{RetryStatuses: []int{403, 429, 502, 503, 504}, MaxAttempts: 3}
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
		if len(req.ConfigYAML) > 0 {
			if err := yaml.Unmarshal(req.ConfigYAML, &cfg); err != nil {
				return err
			}
		}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if len(cfg.RetryStatuses) == 0 {
		cfg.RetryStatuses = []int{403, 429, 502, 503, 504}
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	currentConfig.Store(cfg)
	return nil
}

func validateConfig(cfg comboConfig) error {
	if cfg.Enabled && len(cfg.ModelChains) == 0 {
		return fmt.Errorf("model_chains must configure at least one combo")
	}
	for alias, candidates := range cfg.ModelChains {
		if strings.TrimSpace(alias) == "" || alias != strings.TrimSpace(alias) || len(candidates) == 0 {
			return fmt.Errorf("combo alias must be non-empty, trimmed, and have candidates")
		}
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.Model) == "" || candidate.Model != strings.TrimSpace(candidate.Model) {
				return fmt.Errorf("combo %q contains a candidate with an empty or untrimmed model", alias)
			}
			if candidate.Provider != "" && candidate.Provider != strings.TrimSpace(candidate.Provider) {
				return fmt.Errorf("combo %q contains an untrimmed provider", alias)
			}
		}
	}
	if cfg.MaxAttempts < 1 || cfg.MaxAttempts > 20 {
		return fmt.Errorf("max_attempts must be between 1 and 20")
	}
	for _, code := range cfg.RetryStatuses {
		if code != http.StatusForbidden && code != http.StatusTooManyRequests && (code < 500 || code > 599) {
			return fmt.Errorf("retry_statuses only permits 403, 429, and 5xx codes")
		}
	}
	return nil
}

func loadedConfig() comboConfig {
	if raw := currentConfig.Load(); raw != nil {
		if cfg, ok := raw.(comboConfig); ok {
			return cfg
		}
	}
	return comboConfig{}
}

func pluginRegistration() registration {
	formats := []string{"openai", "openai-response", "claude", "gemini", "codex", "antigravity", "interactions"}
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata:      pluginapi.Metadata{Name: pluginID, Version: "0.4.2", Author: "ivanarifin", GitHubRepository: "https://github.com/ivanarifin/CPA-Combo-Plugin"},
		Capabilities:  registrationCapabilities{ModelRegistrar: true, ModelRouter: true, Executor: true, ExecutorModelScope: string(pluginapi.ExecutorModelScopeStatic), ExecutorInputFormats: formats, ExecutorOutputFormats: formats, ManagementAPI: true},
	}
}

func registerManagement() ([]byte, error) {
	return okEnvelope(pluginapi.ManagementRegistrationResponse{
		Resources: []pluginapi.ResourceRoute{{
			Path:        "/editor",
			Menu:        "Combo Router",
			Description: "Create combo aliases and order their fallback model candidates.",
		}},
	})
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.Method != http.MethodGet || !strings.HasSuffix(strings.TrimRight(req.Path, "/"), "/editor") {
		return okEnvelope(pluginapi.ManagementResponse{StatusCode: http.StatusNotFound, Body: []byte("not found")})
	}
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}, "X-Content-Type-Options": []string{"nosniff"}, "Referrer-Policy": []string{"no-referrer"}, "Content-Security-Policy": []string{"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"}},
		Body:       []byte(comboDashboardHTML),
	})
}

func registerModels() ([]byte, error) {
	cfg := loadedConfig()
	models := make([]pluginapi.ModelInfo, 0, len(cfg.ModelChains))
	if cfg.Enabled {
		aliases := make([]string, 0, len(cfg.ModelChains))
		for alias := range cfg.ModelChains {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			models = append(models, pluginapi.ModelInfo{
				ID:                         alias,
				Object:                     "model",
				OwnedBy:                    pluginID,
				DisplayName:                alias,
				Name:                       alias,
				SupportedGenerationMethods: []string{"chat"},
				UserDefined:                true,
			})
		}
	}
	return okEnvelope(pluginapi.ModelRegistrationResponse{Provider: executorProviderID, Models: models})
}

func candidateProtocol(candidate comboCandidate, fallback string) string {
	if protocol := strings.TrimSpace(candidate.Protocol); protocol != "" {
		return protocol
	}
	return fallback
}

func routeDecision(cfg comboConfig, req pluginapi.ModelRouteRequest) pluginapi.ModelRouteResponse {
	if !cfg.Enabled {
		return pluginapi.ModelRouteResponse{Handled: false, Reason: "combo_plugin_disabled"}
	}
	if _, ok := cfg.ModelChains[strings.TrimSpace(req.RequestedModel)]; !ok {
		return pluginapi.ModelRouteResponse{Handled: false, Reason: "combo_not_configured"}
	}
	return pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, Reason: "combo_fallback_chain"}
}

func routeModel(raw []byte) ([]byte, error) {
	var req rpcModelRouteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return okEnvelope(routeDecision(loadedConfig(), req.ModelRouteRequest))
}

func rewriteRequestModel(body []byte, model string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode request JSON: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("decode request JSON: expected an object")
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	payload["model"] = encoded
	return json.Marshal(payload)
}

func candidatesFor(req pluginapi.ExecutorRequest, cfg comboConfig) ([]comboCandidate, []byte, error) {
	body := req.OriginalRequest
	if len(body) == 0 {
		body = req.Payload
	}
	// ExecutorRequest.Model is the requested model alias preserved by CPA. The
	// executor adapter may normalize the payload before invoking this plugin.
	alias := strings.TrimSpace(req.Model)
	if requested, ok := req.Metadata[coreexecutor.RequestedModelMetadataKey].(string); ok && strings.TrimSpace(requested) != "" {
		alias = strings.TrimSpace(requested)
	}
	if alias == "" {
		var clientReq struct {
			Model string `json:"model"`
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &clientReq); err != nil {
				return nil, nil, fmt.Errorf("decode original request: %w", err)
			}
		}
		alias = strings.TrimSpace(clientReq.Model)
	}
	candidates := cfg.ModelChains[alias]
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("no combo configured for model %q", alias)
	}
	return candidates, append([]byte(nil), body...), nil
}

func retryable(err error, statuses []int) bool {
	status, ok := AsStatusError(err)
	return ok && isRetryableStatus(status.Code, statuses)
}

func invalidHTTPStatus(operation string, code int) error {
	if code > 0 {
		return StatusError{Code: code, Message: fmt.Sprintf("%s returned HTTP status %d", operation, code)}
	}
	return fmt.Errorf("%s returned no HTTP status", operation)
}

func executeFallback(ctx context.Context, candidates []comboCandidate, cfg comboConfig, body []byte, callbackID string, call hostModelCall) (pluginapi.HostModelExecutionResponse, error) {
	if len(candidates) == 0 {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("combo has no candidates")
	}
	limit := cfg.MaxAttempts
	if limit <= 0 {
		limit = 3
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	var lastErr error
	for _, candidate := range candidates[:limit] {
		if err := ctx.Err(); err != nil {
			return pluginapi.HostModelExecutionResponse{}, err
		}
		request, err := rewriteRequestModel(body, candidate.Model)
		if err != nil {
			return pluginapi.HostModelExecutionResponse{}, err
		}
		resp, err := call(ctx, candidate, request, callbackID)
		if err != nil {
			lastErr = err
			if retryable(err, cfg.RetryStatuses) {
				continue
			}
			return pluginapi.HostModelExecutionResponse{}, err
		}
		if isRetryableStatus(resp.StatusCode, cfg.RetryStatuses) {
			lastErr = StatusError{Code: resp.StatusCode}
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return pluginapi.HostModelExecutionResponse{}, invalidHTTPStatus("host model execution", resp.StatusCode)
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("combo candidates exhausted")
	}
	return pluginapi.HostModelExecutionResponse{}, lastErr
}

func isRetryableStatus(code int, statuses []int) bool {
	for _, status := range statuses {
		if code == status {
			return true
		}
	}
	return false
}

func execute(raw []byte) ([]byte, error) {
	return executeWithHost(raw, callHost)
}

func executeWithHost(raw []byte, hostCall rpcHostCall) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cfg := loadedConfig()
	if !cfg.Enabled {
		return errorEnvelope("executor_disabled", "combo plugin is disabled"), nil
	}
	candidates, body, err := candidatesFor(req.ExecutorRequest, cfg)
	if err != nil {
		status, _ := AsStatusError(err)
		return errorEnvelope("executor_error", err.Error(), status.Code), nil
	}
	resp, err := executeFallback(context.Background(), candidates, cfg, body, req.HostCallbackID, func(ctx context.Context, c comboCandidate, b []byte, callback string) (pluginapi.HostModelExecutionResponse, error) {
		return executeHostModelWithHost(ctx, req, c, b, callback, hostCall)
	})
	if err != nil {
		status, _ := AsStatusError(err)
		return errorEnvelope("executor_error", err.Error(), status.Code), nil
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers})
}

func executeHostModel(ctx context.Context, req rpcExecutorRequest, candidate comboCandidate, body []byte, callbackID string) (pluginapi.HostModelExecutionResponse, error) {
	return executeHostModelWithHost(ctx, req, candidate, body, callbackID, callHost)
}

func executeHostModelWithHost(ctx context.Context, req rpcExecutorRequest, candidate comboCandidate, body []byte, callbackID string, hostCall rpcHostCall) (pluginapi.HostModelExecutionResponse, error) {
	raw, err := hostCall(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{EntryProtocol: req.SourceFormat, ExitProtocol: candidateProtocol(candidate, req.SourceFormat), Model: candidate.Model, ForcedProvider: candidate.Provider, Body: body, Headers: req.Headers, Query: req.Query, Alt: req.Alt, CallerScope: "plugin-executor"},
		HostCallbackID:            callbackID,
	})
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, decodeStatusError(err)
	}
	var resp pluginapi.HostModelExecutionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, invalidHTTPStatus("host model execution", resp.StatusCode)
	}
	return resp, nil
}

func executeStream(raw []byte) ([]byte, error) {
	return executeStreamWithHost(raw, callHost)
}

func executeStreamWithHost(raw []byte, hostCall rpcHostCall) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.StreamID) == "" || strings.TrimSpace(req.HostCallbackID) == "" {
		return errorEnvelope("executor_error", "stream_id and host_callback_id are required"), nil
	}
	cfg := loadedConfig()
	if !cfg.Enabled {
		return errorEnvelope("executor_disabled", "combo plugin is disabled"), nil
	}
	candidates, body, err := candidatesFor(req.ExecutorRequest, cfg)
	if err != nil {
		status, _ := AsStatusError(err)
		return errorEnvelope("executor_error", err.Error(), status.Code), nil
	}
	if _, err := rewriteRequestModel(body, candidates[0].Model); err != nil {
		return errorEnvelope("executor_error", err.Error()), nil
	}
	go func() {
		err := executeStreamFallback(context.Background(), candidates, cfg, func(ctx context.Context, c comboCandidate, emit func([]byte) error) error {
			return executeCandidateStreamWithHost(ctx, req, c, body, req.HostCallbackID, emit, hostCall)
		}, func(b []byte) error { return emitPluginStreamChunkWithHost(req.StreamID, b, hostCall) })
		closePluginStreamWithHost(req.StreamID, err, hostCall)
	}()
	return okEnvelope(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
}

func executeCandidateStream(ctx context.Context, req rpcExecutorRequest, candidate comboCandidate, body []byte, callbackID string, emit func([]byte) error) error {
	return executeCandidateStreamWithHost(ctx, req, candidate, body, callbackID, emit, callHost)
}

func executeCandidateStreamWithHost(ctx context.Context, req rpcExecutorRequest, candidate comboCandidate, body []byte, callbackID string, emit func([]byte) error, hostCall rpcHostCall) error {
	request, err := rewriteRequestModel(body, candidate.Model)
	if err != nil {
		return err
	}
	raw, err := hostCall(pluginabi.MethodHostModelExecuteStream, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{EntryProtocol: req.SourceFormat, ExitProtocol: candidateProtocol(candidate, req.SourceFormat), Model: candidate.Model, ForcedProvider: candidate.Provider, Stream: true, Body: request, Headers: req.Headers, Query: req.Query, Alt: req.Alt, CallerScope: "plugin-executor"},
		HostCallbackID:            callbackID,
	})
	if err != nil {
		return err
	}
	var stream pluginapi.HostModelStreamResponse
	if err := json.Unmarshal(raw, &stream); err != nil {
		return err
	}
	if stream.StatusCode < http.StatusOK || stream.StatusCode >= http.StatusMultipleChoices {
		if stream.StreamID != "" {
			_, _ = hostCall(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: stream.StreamID})
		}
		return invalidHTTPStatus("host model stream", stream.StatusCode)
	}
	if stream.StreamID == "" {
		return fmt.Errorf("host returned empty stream id")
	}
	defer func() {
		_, _ = hostCall(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: stream.StreamID})
	}()
	for {
		raw, err := hostCall(pluginabi.MethodHostModelStreamRead, struct {
			pluginapi.HostModelStreamReadRequest
			HostCallbackID string `json:"host_callback_id,omitempty"`
		}{HostModelStreamReadRequest: pluginapi.HostModelStreamReadRequest{StreamID: stream.StreamID}, HostCallbackID: callbackID})
		if err != nil {
			return err
		}
		var chunk pluginapi.HostModelStreamReadResponse
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return err
		}
		if chunk.Error != "" {
			return StatusErrorFromMessage(chunk.Error)
		}
		if len(chunk.Payload) > 0 {
			if err := emit(chunk.Payload); err != nil {
				return err
			}
		}
		if chunk.Done {
			return nil
		}
	}
}

func executeStreamFallback(ctx context.Context, candidates []comboCandidate, cfg comboConfig, run candidateStreamCall, emit func([]byte) error) error {
	if len(candidates) == 0 {
		return fmt.Errorf("combo has no candidates")
	}
	limit := cfg.MaxAttempts
	if limit <= 0 {
		limit = 3
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	var lastErr error
	for _, candidate := range candidates[:limit] {
		if err := ctx.Err(); err != nil {
			return err
		}
		emitted := false
		err := run(ctx, candidate, func(chunk []byte) error {
			if len(chunk) == 0 {
				return nil
			}
			emitted = true
			return emit(chunk)
		})
		if err == nil {
			return nil
		}
		lastErr = err
		if emitted || !retryable(err, cfg.RetryStatuses) {
			return err
		}
	}
	if lastErr == nil {
		return fmt.Errorf("combo candidates exhausted")
	}
	return lastErr
}

func StatusErrorFromMessage(message string) error {
	var status struct {
		StatusCode int `json:"status_code"`
		HTTPStatus int `json:"http_status"`
	}
	if json.Unmarshal([]byte(message), &status) == nil {
		if status.StatusCode > 0 {
			return StatusError{Code: status.StatusCode, Message: message}
		}
		if status.HTTPStatus > 0 {
			return StatusError{Code: status.HTTPStatus, Message: message}
		}
	}
	if message == "" {
		return fmt.Errorf("upstream stream failed")
	}
	return errors.New(message)
}
func emitPluginStreamChunk(id string, payload []byte) error {
	return emitPluginStreamChunkWithHost(id, payload, callHost)
}
func emitPluginStreamChunkWithHost(id string, payload []byte, hostCall rpcHostCall) error {
	_, err := hostCall(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{StreamID: id, Payload: payload})
	return err
}
func closePluginStream(id string, err error) {
	closePluginStreamWithHost(id, err, callHost)
}
func closePluginStreamWithHost(id string, err error, hostCall rpcHostCall) {
	req := rpcStreamCloseRequest{StreamID: id}
	if err != nil {
		req.Error = err.Error()
	}
	_, _ = hostCall(pluginabi.MethodHostStreamClose, req)
}
func closeHostStream(id string) error {
	_, err := callHost(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: id})
	return err
}

func callHost(method string, payload any) (json.RawMessage, error) {
	request, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(request) > 0 {
		ptr := C.CBytes(request)
		if ptr == nil {
			return nil, fmt.Errorf("allocate host callback request")
		}
		defer C.free(ptr)
		requestPtr = (*C.uint8_t)(ptr)
	}
	code := C.call_host_api(cMethod, requestPtr, C.size_t(len(request)), &response)
	var raw []byte
	if response.ptr != nil && response.len > 0 {
		raw = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(code))
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode host callback %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			if env.Error.HTTPStatus > 0 {
				return nil, StatusError{Code: env.Error.HTTPStatus, Message: env.Error.Message}
			}
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if code != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(code))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}
func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}
func errorEnvelope(code, message string, status ...int) []byte {
	httpStatus := 0
	if len(status) > 0 {
		httpStatus = status[0]
	}
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: httpStatus}})
	return raw
}
func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
