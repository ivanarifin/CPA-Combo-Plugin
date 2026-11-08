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
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const pluginID = "cpa-combo-router"
const executorProviderID = "cpa-combo-router-executor"
const smartSelectorTimeout = 10 * time.Second
const smartSelectorMaxOutputTokens = 512
const smartSelectorMaxRequestBytes = 8 * 1024
const defaultFirstByteTimeoutSeconds = 120

var defaultRetryStatuses = []int{403, 429, 500, 502, 503, 504}
var sameModelRetryDelay = time.Second

// errCandidateTimeout means a candidate went silent, so moving on is always worthwhile,
// whatever retry_statuses says.
var errCandidateTimeout = errors.New("model sent no response before the configured timeout")
var errCandidateAbandoned = errors.New("model attempt was abandoned after its timeout")

var currentConfig atomic.Value
var requestSequence atomic.Uint64

func newRequestCorrelationID() string {
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), requestSequence.Add(1))
}

func logComboEvent(event, requestID, alias, fields string) {
	log.Printf("combo-router event=%s request_id=%q alias=%q%s", event, requestID, alias, fields)
}

func safeErrorKind(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if status, ok := AsStatusError(err); ok {
		return fmt.Sprintf("http_%d", status.Code)
	}
	return "upstream_error"
}

func outcomeForError(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}

func requestHasStream(body []byte) bool {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	stream, _ := payload["stream"].(bool)
	return stream
}

func observedFusionCall(requestID, alias string, judge comboCandidate, call hostModelCall) hostModelCall {
	return func(ctx context.Context, candidate comboCandidate, body []byte, callbackID string) (pluginapi.HostModelExecutionResponse, error) {
		isJudge := candidate == judge
		event := "fusion_judge_fallback"
		if isJudge {
			event = "fusion_judge"
		}
		start := time.Now()
		logComboEvent(event+"_start", requestID, alias, fmt.Sprintf(" model=%q provider=%q", candidate.Model, candidate.Provider))
		response, err := call(ctx, candidate, body, callbackID)
		if err == nil && (response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices) {
			err = invalidHTTPStatus("fusion judge execution", response.StatusCode)
		}
		status := response.StatusCode
		if upstreamStatus, ok := AsStatusError(err); ok {
			status = upstreamStatus.Code
		}
		logComboEvent(event+"_result", requestID, alias, fmt.Sprintf(" model=%q provider=%q outcome=%s status_code=%d error_kind=%q elapsed_ms=%d", candidate.Model, candidate.Provider, outcomeForError(err), status, safeErrorKind(err), time.Since(start).Milliseconds()))
		return response, err
	}
}

func observedSmartCall(requestID, alias string, call hostModelCall) hostModelCall {
	return func(ctx context.Context, candidate comboCandidate, body []byte, callbackID string) (pluginapi.HostModelExecutionResponse, error) {
		start := time.Now()
		logComboEvent("smart_candidate_start", requestID, alias, fmt.Sprintf(" model=%q provider=%q", candidate.Model, candidate.Provider))
		response, err := call(ctx, candidate, body, callbackID)
		status := response.StatusCode
		if upstreamStatus, ok := AsStatusError(err); ok {
			status = upstreamStatus.Code
		}
		logComboEvent("smart_candidate_result", requestID, alias, fmt.Sprintf(" model=%q provider=%q outcome=%s status_code=%d error_kind=%q elapsed_ms=%d", candidate.Model, candidate.Provider, outcomeForError(err), status, safeErrorKind(err), time.Since(start).Milliseconds()))
		return response, err
	}
}

func executeObservedFusionStream(ctx context.Context, requestID, alias string, judge comboCandidate, candidates []comboCandidate, cfg comboConfig, run candidateStreamCall, emit func([]byte) error) error {
	return executeStreamFallback(ctx, candidates, cfg, func(ctx context.Context, candidate comboCandidate, emitCandidate func([]byte) error) error {
		event := "fusion_judge_fallback"
		if candidate == judge {
			event = "fusion_judge"
		}
		start := time.Now()
		logComboEvent(event+"_attempt_start", requestID, alias, fmt.Sprintf(" model=%q provider=%q streaming=true", candidate.Model, candidate.Provider))
		err := run(ctx, candidate, emitCandidate)
		status, _ := AsStatusError(err)
		if err == nil {
			status.Code = http.StatusOK
		}
		logComboEvent(event+"_attempt_result", requestID, alias, fmt.Sprintf(" model=%q provider=%q outcome=%s status_code=%d error_kind=%q elapsed_ms=%d", candidate.Model, candidate.Provider, outcomeForError(err), status.Code, safeErrorKind(err), time.Since(start).Milliseconds()))
		return err
	}, emit)
}

type comboConfig struct {
	Enabled       bool                        `yaml:"enabled"`
	ModelChains   map[string][]comboCandidate `yaml:"model_chains"`
	Strategies    map[string]comboStrategy    `yaml:"strategies"`
	RetryStatuses []int                       `yaml:"retry_statuses"`
	MaxAttempts   int                         `yaml:"max_attempts"`
	// FirstByteTimeoutSeconds limits streaming attempts that have another candidate left.
	FirstByteTimeoutSeconds int `yaml:"first_byte_timeout_seconds"`
	// AttemptTimeoutSeconds limits non-streaming attempts that have another candidate left.
	AttemptTimeoutSeconds int `yaml:"attempt_timeout_seconds"`
	// LogPanelErrors adds upstream error text to fusion panel failure logs.
	LogPanelErrors bool `yaml:"log_panel_errors"`
}

type comboStrategy struct {
	Type                string           `yaml:"type"`
	StickyLimit         int              `yaml:"sticky_limit"`
	Selector            comboCandidate   `yaml:"selector"`
	Judge               comboCandidate   `yaml:"judge"`
	ConfidenceThreshold float64          `yaml:"confidence_threshold"`
	VisionEnabled       bool             `yaml:"vision_enabled"`
	VisionCandidates    []comboCandidate `yaml:"vision_candidates"`
	VisionJudge         comboCandidate   `yaml:"vision_judge"`
	// VisionScope is "history" (default, any image still in the request routes to the
	// vision pool) or "latest" (only an image added in the newest turn does; older images
	// are replaced with a text placeholder before the normal pool sees the request).
	VisionScope         string `yaml:"vision_scope"`
	PanelTimeoutSeconds int    `yaml:"panel_timeout_seconds"`
	Quorum              int    `yaml:"quorum"`
	QuorumGraceSeconds  int    `yaml:"quorum_grace_seconds"`
	// FusionRounds is how many proposal rounds panels run (default 2). Later rounds
	// show each panel the anonymized proposals so it can correct or improve its answer.
	FusionRounds int `yaml:"fusion_rounds"`
	// FusionReasoning defaults to true; set false to keep panel reasoning from the judge.
	FusionReasoning        *bool             `yaml:"fusion_reasoning"`
	ReadOnlyTools          []string          `yaml:"read_only_tools"`
	RotatePerRequest       bool              `yaml:"rotate_per_request"`
	SelectorTimeoutSeconds int               `yaml:"selector_timeout_seconds"`
	CandidateHints         map[string]string `yaml:"candidate_hints"`
	KeepHistoryReasoning   bool              `yaml:"keep_history_reasoning"`
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
	StreamID    string           `json:"stream_id"`
	Error       string           `json:"error,omitempty"`
	ErrorDetail *pluginabi.Error `json:"error_detail,omitempty"`
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

type rotationState struct {
	Index    int
	UseCount int
}

var comboRotations = struct {
	sync.Mutex
	states map[string]rotationState
}{states: make(map[string]rotationState)}

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
	cfg := comboConfig{RetryStatuses: append([]int(nil), defaultRetryStatuses...), MaxAttempts: 3, FirstByteTimeoutSeconds: defaultFirstByteTimeoutSeconds}
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
		cfg.RetryStatuses = append([]int(nil), defaultRetryStatuses...)
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	currentConfig.Store(cfg)
	resetComboRotations()
	resetComboTurns()
	return nil
}

func validateConfig(cfg comboConfig) error {
	for alias, candidates := range cfg.ModelChains {
		if strings.TrimSpace(alias) == "" || alias != strings.TrimSpace(alias) || len(candidates) == 0 {
			return fmt.Errorf("combo alias must be non-empty, trimmed, and have candidates")
		}
		if err := validateCandidatePool(alias, "candidate", candidates); err != nil {
			return err
		}
	}
	if cfg.MaxAttempts < 1 || cfg.MaxAttempts > 20 {
		return fmt.Errorf("max_attempts must be between 1 and 20")
	}
	if cfg.FirstByteTimeoutSeconds != 0 && (cfg.FirstByteTimeoutSeconds < 5 || cfg.FirstByteTimeoutSeconds > 1800) {
		return fmt.Errorf("first_byte_timeout_seconds must be 0 or between 5 and 1800")
	}
	if cfg.AttemptTimeoutSeconds != 0 && (cfg.AttemptTimeoutSeconds < 5 || cfg.AttemptTimeoutSeconds > 1800) {
		return fmt.Errorf("attempt_timeout_seconds must be 0 or between 5 and 1800")
	}
	for alias, strategy := range cfg.Strategies {
		if strings.TrimSpace(alias) == "" || alias != strings.TrimSpace(alias) {
			return fmt.Errorf("strategy alias must be non-empty and trimmed")
		}
		if _, exists := cfg.ModelChains[alias]; !exists {
			return fmt.Errorf("strategy %q has no matching model chain", alias)
		}
		strategyType := normalizedStrategy(strategy.Type)
		if strategyType == "" {
			return fmt.Errorf("strategy %q has an unsupported type %q", alias, strategy.Type)
		}
		if strategy.StickyLimit < 0 || strategy.StickyLimit > 1000 {
			return fmt.Errorf("strategy %q sticky_limit must be between 0 and 1000", alias)
		}
		if strategy.ConfidenceThreshold != 0 && (strategy.ConfidenceThreshold < 0.5 || strategy.ConfidenceThreshold > 0.95) {
			return fmt.Errorf("strategy %q confidence_threshold must be between 0.5 and 0.95", alias)
		}
		if strategyType == "fusion" && len(cfg.ModelChains[alias]) > 20 {
			return fmt.Errorf("strategy %q fusion supports at most 20 panel models", alias)
		}
		if len(strategy.VisionCandidates) > 0 {
			if err := validateCandidatePool(alias, "vision candidate", strategy.VisionCandidates); err != nil {
				return err
			}
		}
		if normalizedVisionScope(strategy.VisionScope) == "" {
			return fmt.Errorf("strategy %q vision_scope must be history or latest", alias)
		}
		if strategy.VisionEnabled && len(strategy.VisionCandidates) == 0 {
			return fmt.Errorf("strategy %q enables vision routing but has no vision_candidates", alias)
		}
		if strategyType == "fusion" && len(strategy.VisionCandidates) > 20 {
			return fmt.Errorf("strategy %q fusion supports at most 20 vision panel models", alias)
		}
		if strategy.VisionJudge.Model != "" {
			if err := validateStrategyModel(alias, "vision judge", strategy.VisionJudge); err != nil {
				return err
			}
		} else if strategy.VisionJudge.Provider != "" || strategy.VisionJudge.Protocol != "" {
			return fmt.Errorf("strategy %q vision judge provider and protocol require a vision judge model", alias)
		}
		if strategyType == "smart" {
			if err := validateStrategyModel(alias, "selector", strategy.Selector); err != nil {
				return err
			}
			if strategy.SelectorTimeoutSeconds != 0 && (strategy.SelectorTimeoutSeconds < 1 || strategy.SelectorTimeoutSeconds > 120) {
				return fmt.Errorf("strategy %q selector_timeout_seconds must be between 1 and 120", alias)
			}
		}
		if strategyType == "fusion" {
			if strategy.Judge.Model != "" {
				if err := validateStrategyModel(alias, "judge", strategy.Judge); err != nil {
					return err
				}
			} else if strategy.Judge.Provider != "" || strategy.Judge.Protocol != "" {
				return fmt.Errorf("strategy %q judge provider and protocol require a judge model", alias)
			}
			if err := validateFusionOptions(alias, strategy); err != nil {
				return err
			}
		}
	}
	for _, code := range cfg.RetryStatuses {
		if code < 400 || code > 599 {
			return fmt.Errorf("retry_statuses must be HTTP 4xx or 5xx codes")
		}
	}
	return nil
}

func normalizedStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "fallback":
		return "fallback"
	case "round-robin", "round_robin":
		return "round-robin"
	case "smart", "smart-router":
		return "smart"
	case "fusion", "judge":
		return "fusion"
	default:
		return ""
	}
}

func validateCandidatePool(alias, name string, candidates []comboCandidate) error {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Model) == "" || candidate.Model != strings.TrimSpace(candidate.Model) {
			return fmt.Errorf("combo %q contains a %s with an empty or untrimmed model", alias, name)
		}
		if candidate.Provider != "" && candidate.Provider != strings.TrimSpace(candidate.Provider) {
			return fmt.Errorf("combo %q contains a %s with an untrimmed provider", alias, name)
		}
	}
	return nil
}

func validateStrategyModel(alias, name string, candidate comboCandidate) error {
	if strings.TrimSpace(candidate.Model) == "" || candidate.Model != strings.TrimSpace(candidate.Model) {
		return fmt.Errorf("strategy %q %s model must be non-empty and trimmed", alias, name)
	}
	if candidate.Provider != strings.TrimSpace(candidate.Provider) {
		return fmt.Errorf("strategy %q %s provider must be trimmed", alias, name)
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
		Metadata:      pluginapi.Metadata{Name: pluginID, Version: "0.8.1", Author: "ivanarifin", GitHubRepository: "https://github.com/ivanarifin/CPA-Combo-Plugin"},
		Capabilities:  registrationCapabilities{ModelRegistrar: true, ModelRouter: true, Executor: true, ExecutorModelScope: string(pluginapi.ExecutorModelScopeStatic), ExecutorInputFormats: formats, ExecutorOutputFormats: formats, ManagementAPI: true},
	}
}

func registerManagement() ([]byte, error) {
	return okEnvelope(pluginapi.ManagementRegistrationResponse{
		Resources: []pluginapi.ResourceRoute{{
			Path:        "/editor",
			Menu:        "Combo Router",
			Description: "Create combo aliases, configure routing strategies, and order candidate models.",
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
	alias := requestedAlias(req, body)
	candidates := cfg.ModelChains[alias]
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("no combo configured for model %q", alias)
	}
	return candidates, append([]byte(nil), body...), nil
}

func requestedAlias(req pluginapi.ExecutorRequest, body []byte) string {
	alias := strings.TrimSpace(req.Model)
	if requested, ok := req.Metadata[coreexecutor.RequestedModelMetadataKey].(string); ok && strings.TrimSpace(requested) != "" {
		alias = strings.TrimSpace(requested)
	}
	if alias == "" {
		var clientReq struct {
			Model string `json:"model"`
		}
		if len(body) > 0 && json.Unmarshal(body, &clientReq) == nil {
			alias = strings.TrimSpace(clientReq.Model)
		}
	}
	return alias
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
	attempts := attemptsPerModel(cfg)
	attemptTimeout := time.Duration(cfg.AttemptTimeoutSeconds) * time.Second
	var lastErr error
	for index, candidate := range candidates {
		request, err := rewriteRequestModel(body, candidate.Model)
		if err != nil {
			return pluginapi.HostModelExecutionResponse{}, err
		}
	tries:
		for attempt := 1; attempt <= attempts; attempt++ {
			if attempt > 1 {
				if err := waitBeforeRetry(ctx); err != nil {
					return pluginapi.HostModelExecutionResponse{}, err
				}
			} else if err := ctx.Err(); err != nil {
				return pluginapi.HostModelExecutionResponse{}, err
			}
			finalAttempt := index == len(candidates)-1 && attempt == attempts
			var resp pluginapi.HostModelExecutionResponse
			if attemptTimeout > 0 && !finalAttempt {
				resp, err = callWithAttemptTimeout(ctx, attemptTimeout, call, candidate, request, callbackID)
			} else {
				resp, err = call(ctx, candidate, request, callbackID)
			}
			if err != nil {
				lastErr = err
				if !shouldTryNextCandidate(err, cfg) {
					return pluginapi.HostModelExecutionResponse{}, err
				}
				if isContextOverflow(err) {
					break tries
				}
				continue
			}
			if isRetryableStatus(resp.StatusCode, cfg.RetryStatuses) {
				lastErr = StatusError{Code: resp.StatusCode}
				continue
			}
			if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
				return pluginapi.HostModelExecutionResponse{}, invalidHTTPStatus("host model execution", resp.StatusCode)
			}
			if responseBodyIsEmpty(resp.Body) {
				lastErr = errEmptyResponse
				continue
			}
			return resp, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("combo candidates exhausted")
	}
	return pluginapi.HostModelExecutionResponse{}, lastErr
}

// attemptsPerModel is how many tries each model gets before the chain moves on, so a list
// of 10 models with max_attempts 3 can make up to 30 attempts.
func attemptsPerModel(cfg comboConfig) int {
	if cfg.MaxAttempts <= 0 {
		return 3
	}
	return cfg.MaxAttempts
}

// waitBeforeRetry spaces out tries of the same model, since an instant retry rarely helps.
func waitBeforeRetry(ctx context.Context) error {
	timer := time.NewTimer(sameModelRetryDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func callWithAttemptTimeout(ctx context.Context, timeout time.Duration, call hostModelCall, candidate comboCandidate, request []byte, callbackID string) (pluginapi.HostModelExecutionResponse, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := callHostModelContext(attemptCtx, call, candidate, request, callbackID)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return response, errCandidateTimeout
	}
	return response, err
}

func shouldTryNextCandidate(err error, cfg comboConfig) bool {
	return retryable(err, cfg.RetryStatuses) || errors.Is(err, errCandidateTimeout) || errors.Is(err, errEmptyResponse) || isContextOverflow(err)
}

// isContextOverflow recognizes a request too large for this model. It is a client error
// by status, but the next candidate may have a bigger context window.
func isContextOverflow(err error) bool {
	status, ok := AsStatusError(err)
	if !ok || (status.Code != http.StatusBadRequest && status.Code != http.StatusRequestEntityTooLarge) {
		return false
	}
	message := strings.ToLower(status.Message)
	for _, marker := range []string{"context_length_exceeded", "maximum context length", "context window", "prompt is too long", "input is too long", "too many input tokens", "too many tokens", "input token count", "exceeds the maximum number of tokens", "request_too_large"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// runWithFirstByteLimit gives up on an attempt that stays silent past limit. The attempt
// may still be blocked in the host, so it keeps running detached with its output refused,
// which guarantees a late model can never write into the next candidate's response.
func runWithFirstByteLimit(ctx context.Context, limit time.Duration, candidate comboCandidate, run candidateStreamCall, emit func([]byte) error) error {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	started, abandoned := false, false
	guarded := func(chunk []byte) error {
		mu.Lock()
		if abandoned {
			mu.Unlock()
			return errCandidateAbandoned
		}
		if len(chunk) > 0 {
			started = true
		}
		mu.Unlock()
		return emit(chunk)
	}
	done := make(chan error, 1)
	go func() { done <- run(attemptCtx, candidate, guarded) }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		mu.Lock()
		abandoned = true
		mu.Unlock()
		return ctx.Err()
	case <-timer.C:
	}
	mu.Lock()
	if started {
		mu.Unlock()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	abandoned = true
	mu.Unlock()
	return errCandidateTimeout
}

func isRetryableStatus(code int, statuses []int) bool {
	for _, status := range statuses {
		if code == status {
			return true
		}
	}
	return false
}

func strategyFor(cfg comboConfig, alias string) comboStrategy {
	strategy := cfg.Strategies[alias]
	strategy.Type = normalizedStrategy(strategy.Type)
	if strategy.Type == "" {
		strategy.Type = "fallback"
	}
	if strategy.StickyLimit <= 0 {
		strategy.StickyLimit = 1
	}
	if strategy.ConfidenceThreshold == 0 {
		strategy.ConfidenceThreshold = 0.8
	}
	return strategy
}

func resetComboRotations() {
	comboRotations.Lock()
	comboRotations.states = make(map[string]rotationState)
	comboRotations.Unlock()
}

func rotateCandidates(alias string, candidates []comboCandidate, stickyLimit int) []comboCandidate {
	if len(candidates) < 2 {
		return append([]comboCandidate(nil), candidates...)
	}
	if stickyLimit < 1 {
		stickyLimit = 1
	}
	comboRotations.Lock()
	state := comboRotations.states[alias]
	index := state.Index % len(candidates)
	ordered := append(append([]comboCandidate(nil), candidates[index:]...), candidates[:index]...)
	state.UseCount++
	if state.UseCount >= stickyLimit {
		state.Index = (index + 1) % len(candidates)
		state.UseCount = 0
	}
	comboRotations.states[alias] = state
	comboRotations.Unlock()
	return ordered
}

func prioritizeCandidate(candidates []comboCandidate, selected comboCandidate) []comboCandidate {
	out := make([]comboCandidate, 0, len(candidates))
	found := false
	for _, candidate := range candidates {
		if !found && candidate == selected {
			out = append(out, candidate)
			found = true
		}
	}
	if !found {
		return append([]comboCandidate(nil), candidates...)
	}
	for _, candidate := range candidates {
		if candidate != selected {
			out = append(out, candidate)
		}
	}
	return out
}

func executeWithStrategy(ctx context.Context, alias string, strategy comboStrategy, candidates []comboCandidate, cfg comboConfig, body []byte, callbackID string, call hostModelCall, selectorCall hostModelCall) (pluginapi.HostModelExecutionResponse, error) {
	candidates, strategy, rotationAlias, body, visionRouted, err := requestCandidatePool(alias, strategy, candidates, body)
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	switch strategy.Type {
	case "round-robin":
		if visionRouted {
			logComboEvent("vision_route", newRequestCorrelationID(), alias, fmt.Sprintf(" strategy=%q candidate_count=%d", strategy.Type, len(candidates)))
		}
		pin := roundRobinPin(rotationAlias, strategy, body)
		return executeFallback(ctx, roundRobinOrder(rotationAlias, strategy, candidates, pin), cfg, body, callbackID, pin.observe(call))
	case "smart":
		requestID := newRequestCorrelationID()
		if visionRouted {
			logComboEvent("vision_route", requestID, alias, fmt.Sprintf(" strategy=%q candidate_count=%d", strategy.Type, len(candidates)))
		}
		pin := newComboTurnPin(rotationAlias, body)
		ordered := candidates
		if pinned, ok := pin.pinned(candidates); ok {
			logComboEvent("smart_selection", requestID, alias, fmt.Sprintf(" outcome=sticky model=%q provider=%q", pinned.Model, pinned.Provider))
			ordered = prioritizeCandidate(candidates, pinned)
		} else if selected, ok := selectSmartCandidate(ctx, requestID, alias, strategy, candidates, body, callbackID, selectorCall); ok {
			ordered = prioritizeCandidate(candidates, selected)
		} else {
			logComboEvent("smart_fallback", requestID, alias, " reason=selector_not_confident")
		}
		return executeFallback(ctx, ordered, cfg, body, callbackID, pin.observe(observedSmartCall(requestID, alias, call)))
	case "fusion":
		requestID := newRequestCorrelationID()
		if visionRouted {
			logComboEvent("vision_route", requestID, alias, fmt.Sprintf(" strategy=%q candidate_count=%d", strategy.Type, len(candidates)))
		}
		return executeFusion(ctx, requestID, alias, strategy, candidates, cfg, body, callbackID, call)
	default:
		if visionRouted {
			logComboEvent("vision_route", newRequestCorrelationID(), alias, fmt.Sprintf(" strategy=%q candidate_count=%d", strategy.Type, len(candidates)))
		}
		return executeFallback(ctx, candidates, cfg, body, callbackID, call)
	}
}

func requestCandidatePool(alias string, strategy comboStrategy, candidates []comboCandidate, body []byte) ([]comboCandidate, comboStrategy, string, []byte, bool, error) {
	if !strategy.VisionEnabled {
		return candidates, strategy, alias, body, false, nil
	}
	hasImage := requestHasImagePayload(body)
	if normalizedVisionScope(strategy.VisionScope) == visionScopeLatest {
		if !hasImage {
			return candidates, strategy, alias, body, false, nil
		}
		latest := latestTurnHasImagePayload(body)
		stripped, images, size := stripHistoryImages(body)
		if images > 0 {
			logComboEvent("vision_history_images_stripped", "", alias, fmt.Sprintf(" images=%d bytes=%d scope=latest", images, size))
			body = stripped
		}
		if !latest && images > 0 {
			return candidates, strategy, alias, body, false, nil
		}
		// With no new image and images that could not be proven gone, keep the vision
		// pool rather than send an image payload to models that may reject it.
	} else if !hasImage {
		return candidates, strategy, alias, body, false, nil
	}
	if len(strategy.VisionCandidates) == 0 {
		return nil, strategy, alias, body, false, fmt.Errorf("combo %q received an image request but vision routing has no vision_candidates", alias)
	}
	visionCandidates := append([]comboCandidate(nil), strategy.VisionCandidates...)
	if strategy.Type == "fusion" {
		strategy.Judge = strategy.VisionJudge
	}
	return visionCandidates, strategy, alias + "#vision", body, true, nil
}

const (
	visionScopeHistory = "history"
	visionScopeLatest  = "latest"
)

func normalizedVisionScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", visionScopeHistory:
		return visionScopeHistory
	case visionScopeLatest:
		return visionScopeLatest
	}
	return ""
}

// latestTurnHasImagePayload reports whether the newest turn, after the last assistant
// boundary, adds an image. Images that only exist earlier in the history do not count.
func latestTurnHasImagePayload(body []byte) bool {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	for _, key := range payloadHistoryKeys {
		list, ok := payload[key].([]any)
		if !ok {
			continue
		}
		for _, item := range list[latestTurnStart(list):] {
			if containsImagePayload(item) {
				return true
			}
		}
	}
	return false
}

var payloadHistoryKeys = []string{"messages", "input", "contents"}

const visionHistoryImagePlaceholder = "[image omitted from earlier context]"

// latestTurnStart finds the first message after the last assistant turn, so callers can
// treat everything before it as history and everything after it as the current request.
func latestTurnStart(list []any) int {
	for index := len(list) - 1; index >= 0; index-- {
		item, ok := list[index].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := item["role"].(string); role == "assistant" || role == "model" {
			return index + 1
		}
		switch kind, _ := item["type"].(string); kind {
		case "function_call", "computer_call", "web_search_call", "reasoning":
			return index + 1
		}
	}
	return 0
}

// stripHistoryImages replaces images from earlier turns with text so a model that cannot
// see them still receives a valid request. The newest turn is left alone; the caller only
// strips when that turn added no image.
func stripHistoryImages(body []byte) ([]byte, int, int) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return body, 0, 0
	}
	images, size := 0, 0
	for _, key := range payloadHistoryKeys {
		list, ok := payload[key].([]any)
		if !ok {
			continue
		}
		start := latestTurnStart(list)
		for index := 0; index < start; index++ {
			replaced, removed, bytes := replaceImageParts(list[index])
			if removed > 0 {
				list[index] = replaced
				images += removed
				size += bytes
			}
		}
	}
	if images == 0 {
		return body, 0, 0
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, 0, 0
	}
	return encoded, images, size
}

func replaceImageParts(value any) (any, int, int) {
	switch item := value.(type) {
	case []any:
		images, size := 0, 0
		for index, child := range item {
			replaced, removed, bytes := replaceImageParts(child)
			if removed > 0 {
				item[index] = replaced
				images += removed
				size += bytes
			}
		}
		return item, images, size
	case map[string]any:
		if part, bytes, ok := imagePlaceholderPart(item); ok {
			return part, 1, bytes
		}
		images, size := 0, 0
		for key, child := range item {
			replaced, removed, bytes := replaceImageParts(child)
			if removed > 0 {
				item[key] = replaced
				images += removed
				size += bytes
			}
		}
		return item, images, size
	}
	return value, 0, 0
}

func imagePlaceholderPart(item map[string]any) (map[string]any, int, bool) {
	kind, _ := item["type"].(string)
	switch strings.ToLower(kind) {
	case "image_url", "input_image":
		if !partHasImageSource(item) {
			return nil, 0, false
		}
		textType := "text"
		if strings.EqualFold(kind, "input_image") {
			textType = "input_text"
		}
		return map[string]any{"type": textType, "text": visionHistoryImagePlaceholder}, jsonSize(item), true
	case "image":
		if !partHasImageSource(item) {
			return nil, 0, false
		}
		return map[string]any{"type": "text", "text": visionHistoryImagePlaceholder}, jsonSize(item), true
	}
	for key, child := range item {
		switch strings.ToLower(key) {
		case "image_url", "input_image":
			if hasImageSource(child) {
				return map[string]any{"type": "text", "text": visionHistoryImagePlaceholder}, jsonSize(item), true
			}
		case "inline_data", "inlinedata", "file_data", "filedata":
			if hasImageData(child) {
				return map[string]any{"text": visionHistoryImagePlaceholder}, jsonSize(item), true
			}
		}
	}
	return nil, 0, false
}

func partHasImageSource(item map[string]any) bool {
	for _, key := range []string{"source", "url", "image_url", "data", "file_id"} {
		if hasImageSource(item[key]) {
			return true
		}
	}
	return false
}

func jsonSize(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(encoded)
}

func requestHasImagePayload(body []byte) bool {
	var payload any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	return containsImagePayload(payload)
}

func containsImagePayload(value any) bool {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			if containsImagePayload(child) {
				return true
			}
		}
	case map[string]any:
		kind, _ := item["type"].(string)
		if strings.EqualFold(kind, "image") || strings.EqualFold(kind, "input_image") || strings.EqualFold(kind, "image_url") {
			for _, key := range []string{"source", "url", "image_url", "data", "file_id"} {
				if hasImageSource(item[key]) {
					return true
				}
			}
		}
		for key, child := range item {
			switch strings.ToLower(key) {
			case "image_url", "input_image":
				if hasImageSource(child) {
					return true
				}
			case "inline_data", "inlinedata", "file_data", "filedata":
				if hasImageData(child) {
					return true
				}
			}
			if containsImagePayload(child) {
				return true
			}
		}
	}
	return false
}

func hasImageSource(value any) bool {
	switch item := value.(type) {
	case string:
		return strings.TrimSpace(item) != ""
	case []any:
		return len(item) > 0
	case map[string]any:
		for key, child := range item {
			switch strings.ToLower(key) {
			case "url", "image_url", "data", "file_id", "file_uri":
				if hasImageSource(child) {
					return true
				}
			}
		}
	}
	return false
}

func hasImageData(value any) bool {
	item, ok := value.(map[string]any)
	if !ok {
		return false
	}
	var mimeType string
	var source any
	for key, child := range item {
		switch strings.ToLower(key) {
		case "mime_type", "mimetype", "media_type":
			mimeType, _ = child.(string)
		case "data", "fileuri", "file_uri":
			source = child
		}
	}
	return strings.HasPrefix(strings.ToLower(mimeType), "image/") && hasImageSource(source)
}

func callHostModelContext(ctx context.Context, call hostModelCall, candidate comboCandidate, body []byte, callbackID string) (pluginapi.HostModelExecutionResponse, error) {
	type result struct {
		response pluginapi.HostModelExecutionResponse
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		response, err := call(ctx, candidate, body, callbackID)
		completed <- result{response: response, err: err}
	}()
	select {
	case got := <-completed:
		return got.response, got.err
	case <-ctx.Done():
		return pluginapi.HostModelExecutionResponse{}, ctx.Err()
	}
}

func smartSelectorBody(body []byte, prompt string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode smart selector request: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("smart selector request must be a JSON object")
	}
	source := payload
	payload = map[string]any{"stream": false}
	if model, ok := source["model"]; ok {
		payload["model"] = model
	}
	// Send only what a small routing answer needs: the client's system prompt, reasoning
	// effort, and large output budget would make every selection slow and expensive.
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if _, ok := source[key]; ok {
			payload[key] = smartSelectorMaxOutputTokens
		}
	}
	if _, ok := source["generationConfig"]; ok {
		payload["generationConfig"] = map[string]any{"maxOutputTokens": smartSelectorMaxOutputTokens}
	}
	if _, ok := source["instructions"]; ok {
		payload["instructions"] = "You route requests to models. Return only the requested JSON."
	}
	_, hasMessages := source["messages"]
	originalInput, hasInput := source["input"]
	_, inputWasString := originalInput.(string)
	_, hasContents := source["contents"]
	_, hasPrompt := source["prompt"]
	if hasMessages {
		payload["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
	} else if hasInput {
		if inputWasString {
			payload["input"] = prompt
		} else {
			payload["input"] = []any{map[string]any{"type": "message", "role": "user", "content": prompt}}
		}
	} else if hasContents {
		payload["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}}}
	} else if hasPrompt {
		payload["prompt"] = prompt
	} else {
		payload["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
	}
	compact, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode smart selector request: %w", err)
	}
	return nonStreamingBody(compact)
}

func selectSmartCandidate(ctx context.Context, requestID, alias string, strategy comboStrategy, candidates []comboCandidate, body []byte, callbackID string, call hostModelCall) (comboCandidate, bool) {
	if call == nil || len(candidates) < 2 || strings.TrimSpace(strategy.Selector.Model) == "" {
		logComboEvent("smart_selection", requestID, alias, " outcome=skipped reason=missing_selector_or_candidates")
		return comboCandidate{}, false
	}
	start := time.Now()
	logComboEvent("smart_selector_start", requestID, alias, fmt.Sprintf(" selector_model=%q selector_provider=%q", strategy.Selector.Model, strategy.Selector.Provider))
	var choices []string
	notes := map[string]string{}
	for _, candidate := range candidates {
		label := smartCandidateLabel(candidate)
		choices = append(choices, label)
		if hint := strategy.CandidateHints[candidate.Model]; hint != "" {
			notes[label] = hint
		}
	}
	request, _ := fusionTruncate(latestUserText(body), smartSelectorMaxRequestBytes)
	selection := map[string]any{
		"instruction": "Select the best candidate for the user's request. Return only JSON: {\"choice\":\"candidate\",\"confidence\":0.0}. Choose a value exactly from candidates.",
		"candidates":  choices,
		"request":     request,
	}
	if len(notes) > 0 {
		selection["candidate_notes"] = notes
	}
	prompt, _ := json.Marshal(selection)
	selectionBody, err := smartSelectorBody(body, string(prompt))
	if err != nil {
		logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=request_error error_kind=%q elapsed_ms=%d", safeErrorKind(err), time.Since(start).Milliseconds()))
		return comboCandidate{}, false
	}
	selectionBody, err = rewriteRequestModel(selectionBody, strategy.Selector.Model)
	if err != nil {
		logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=request_error error_kind=%q elapsed_ms=%d", safeErrorKind(err), time.Since(start).Milliseconds()))
		return comboCandidate{}, false
	}
	timeout := smartSelectorTimeout
	if strategy.SelectorTimeoutSeconds > 0 {
		timeout = time.Duration(strategy.SelectorTimeoutSeconds) * time.Second
	}
	selectionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := callHostModelContext(selectionCtx, call, strategy.Selector, selectionBody, callbackID)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		status, _ := AsStatusError(err)
		if status.Code == 0 {
			status.Code = response.StatusCode
		}
		logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=error status_code=%d error_kind=%q elapsed_ms=%d", status.Code, safeErrorKind(err), time.Since(start).Milliseconds()))
		return comboCandidate{}, false
	}
	choice, ok := parseSmartSelection(response.Body)
	if !ok {
		logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=invalid_response elapsed_ms=%d", time.Since(start).Milliseconds()))
		return comboCandidate{}, false
	}
	if choice.Confidence < strategy.ConfidenceThreshold {
		logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=low_confidence confidence=%.3f threshold=%.3f elapsed_ms=%d", choice.Confidence, strategy.ConfidenceThreshold, time.Since(start).Milliseconds()))
		return comboCandidate{}, false
	}
	for _, candidate := range candidates {
		if choice.Choice == candidate.Model || choice.Choice == candidate.Provider+"/"+candidate.Model {
			logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=selected model=%q provider=%q confidence=%.3f elapsed_ms=%d", candidate.Model, candidate.Provider, choice.Confidence, time.Since(start).Milliseconds()))
			return candidate, true
		}
	}
	logComboEvent("smart_selector_result", requestID, alias, fmt.Sprintf(" outcome=unknown_candidate confidence=%.3f elapsed_ms=%d", choice.Confidence, time.Since(start).Milliseconds()))
	return comboCandidate{}, false
}

func smartCandidateLabel(candidate comboCandidate) string {
	if candidate.Provider == "" {
		return candidate.Model
	}
	return candidate.Provider + "/" + candidate.Model
}

type smartSelection struct {
	Choice     string  `json:"choice"`
	Model      string  `json:"model"`
	Confidence float64 `json:"confidence"`
}

func parseSmartSelection(raw []byte) (smartSelection, bool) {
	text := responseText(raw)
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return smartSelection{}, false
	}
	var result smartSelection
	if err := json.Unmarshal([]byte(text[start:end+1]), &result); err != nil {
		return smartSelection{}, false
	}
	if result.Choice == "" {
		result.Choice = result.Model
	}
	return result, result.Choice != "" && result.Confidence >= 0 && result.Confidence <= 1
}

func valueText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok {
				if thought, _ := block["thought"].(bool); thought {
					continue
				}
				if _, isToolCall := block["functionCall"]; isToolCall {
					continue
				}
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func appendPromptTurn(body []byte, text string) []byte {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return body
	}
	appendTo := func(key string, turn any) bool {
		var items []json.RawMessage
		if json.Unmarshal(payload[key], &items) != nil {
			return false
		}
		encoded, err := json.Marshal(turn)
		if err != nil {
			return false
		}
		items = append(items, encoded)
		payload[key], _ = json.Marshal(items)
		return true
	}
	if appendTo("messages", map[string]any{"role": "user", "content": text}) || appendTo("input", map[string]any{"role": "user", "content": text}) {
		encoded, _ := json.Marshal(payload)
		return encoded
	}
	var inputText string
	if json.Unmarshal(payload["input"], &inputText) == nil && payload["input"] != nil {
		payload["input"] = mustJSON(inputText + "\n\n" + text)
		encoded, _ := json.Marshal(payload)
		return encoded
	}
	var originalPrompt string
	if json.Unmarshal(payload["prompt"], &originalPrompt) == nil && payload["prompt"] != nil {
		payload["prompt"] = mustJSON(originalPrompt + "\n\n" + text)
		encoded, _ := json.Marshal(payload)
		return encoded
	}
	var contents []json.RawMessage
	if json.Unmarshal(payload["contents"], &contents) == nil && payload["contents"] != nil {
		contents = append(contents, mustJSON(map[string]any{"role": "user", "parts": []any{map[string]string{"text": text}}}))
		payload["contents"], _ = json.Marshal(contents)
		encoded, _ := json.Marshal(payload)
		return encoded
	}
	payload["messages"] = mustJSON([]any{map[string]string{"role": "user", "content": text}})
	encoded, _ := json.Marshal(payload)
	return encoded
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func responseText(raw []byte) string {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return string(raw)
	}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				if text := valueText(message["content"]); text != "" {
					return text
				}
			}
		}
	}
	if text := valueText(payload["output_text"]); text != "" {
		return text
	}
	if output, ok := payload["output"].([]any); ok {
		var parts []string
		for _, raw := range output {
			item, _ := raw.(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType != "" && itemType != "message" {
				continue
			}
			if text := responseOutputText(item["content"]); text != "" {
				parts = append(parts, text)
			}
		}
		if text := strings.Join(parts, "\n"); text != "" {
			return text
		}
	}
	if content, ok := payload["content"].([]any); ok {
		return valueText(content)
	}
	if candidates, ok := payload["candidates"].([]any); ok && len(candidates) > 0 {
		if candidate, ok := candidates[0].(map[string]any); ok {
			if content, ok := candidate["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok {
					return valueText(parts)
				}
			}
		}
	}
	return ""
}

func responseOutputText(value any) string {
	blocks, ok := value.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block["type"] != "output_text" {
			continue
		}
		if text, ok := block["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func prepareFusionPrompt(ctx context.Context, strategy comboStrategy, candidates []comboCandidate, body []byte, callbackID string, call hostModelCall) ([]byte, comboCandidate, error) {
	return prepareFusionPromptObserved(ctx, newRequestCorrelationID(), "", strategy, candidates, body, callbackID, call)
}

func prepareFusionPromptObserved(ctx context.Context, requestID, alias string, strategy comboStrategy, candidates []comboCandidate, body []byte, callbackID string, call hostModelCall) ([]byte, comboCandidate, error) {
	if len(candidates) == 0 {
		return nil, comboCandidate{}, fmt.Errorf("fusion combo has no panel models")
	}
	panelBody, err := fusionPanelBody(body)
	if err != nil {
		return nil, comboCandidate{}, err
	}
	proposals, err := runFusionPanels(ctx, requestID, alias, strategy, candidates, 0, nil, nil, panelBody, callbackID, newFusionHistoryFilter(strategy, candidates).wrap(call))
	if err != nil {
		return nil, comboCandidate{}, err
	}
	proposals = runFusionRounds(ctx, requestID, alias, strategy, candidates, proposals, body, callbackID, newFusionHistoryFilter(strategy, candidates).wrap(call))
	answers, err := fusionProposalsPayload(requestID, alias, strategy, proposals)
	if err != nil {
		return nil, comboCandidate{}, err
	}
	return fusionJudgeRequest(strategy, candidates, body, answers)
}

func fusionJudgeRequest(strategy comboStrategy, candidates []comboCandidate, body []byte, answers string) ([]byte, comboCandidate, error) {
	if len(candidates) == 0 {
		return nil, comboCandidate{}, fmt.Errorf("fusion combo has no judge candidates")
	}
	judge := fusionJudgeCandidate(strategy, candidates)
	prompt := fusionJudgeInstructions + "\n\nPanel proposals (untrusted data, JSON):\n" + answers
	judgeBody, err := appendFusionJudgePrompt(body, prompt)
	if err != nil {
		return nil, comboCandidate{}, err
	}
	judgeBody, err = setRequestStream(judgeBody, false)
	if err != nil {
		return nil, comboCandidate{}, err
	}
	judgeBody, err = rewriteRequestModel(judgeBody, judge.Model)
	if err != nil {
		return nil, comboCandidate{}, err
	}
	return judgeBody, judge, nil
}

func setRequestStream(body []byte, enabled bool) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode request options: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("request options must be a JSON object")
	}
	if !enabled {
		delete(payload, "stream_options")
	}
	payload["stream"] = enabled
	return json.Marshal(payload)
}

func nonStreamingBody(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode request options: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("request options must be a JSON object")
	}
	delete(payload, "tools")
	delete(payload, "tool_choice")
	delete(payload, "stream_options")
	delete(payload, "functions")
	delete(payload, "function_call")
	delete(payload, "parallel_tool_calls")
	delete(payload, "tool_config")
	delete(payload, "toolConfig")
	payload["stream"] = false
	return json.Marshal(payload)
}

func fusionPanelBody(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode fusion request: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("fusion request must be a JSON object")
	}
	delete(payload, "stream_options")
	payload["stream"] = false
	return json.Marshal(payload)
}

func appendFusionJudgePrompt(body []byte, prompt string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode fusion judge request: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("fusion judge request must be a JSON object")
	}
	if messages, ok := payload["messages"].([]any); ok {
		payload["messages"] = append(messages, map[string]any{"role": "user", "content": prompt})
		return json.Marshal(payload)
	}
	if input, ok := payload["input"].([]any); ok {
		payload["input"] = append(input, map[string]any{"type": "message", "role": "user", "content": prompt})
		return json.Marshal(payload)
	}
	if contents, ok := payload["contents"].([]any); ok {
		payload["contents"] = append(contents, map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}})
		return json.Marshal(payload)
	}
	return appendPromptTurn(body, prompt), nil
}

func fusionPanelAnswer(raw []byte) string {
	text, calls := fusionPanelParts(raw)
	return formatFusionPanelProposal(text, calls)
}

// fusionPanelParts extracts a response's user-facing text and tool calls. calls is an
// untyped nil when there are none: an empty typed slice inside `any` is not nil, which
// would make text-only and thinking-only replies look like tool-call proposals.
func fusionPanelParts(raw []byte) (string, any) {
	text, calls, _ := fusionPanelPartsWithReasoning(raw)
	return text, calls
}

func normalizeFusionCalls(calls any) any {
	if list, ok := calls.([]any); ok && len(list) == 0 {
		return nil
	}
	return calls
}

func formatFusionPanelProposal(text string, calls any) string {
	if calls == nil {
		return strings.TrimSpace(text)
	}
	proposal := map[string]any{"tool_calls": calls}
	if strings.TrimSpace(text) != "" {
		proposal["content"] = strings.TrimSpace(text)
	}
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return strings.TrimSpace(text)
	}
	return string(encoded)
}

func fusionJudgeOrder(judge comboCandidate, candidates []comboCandidate) []comboCandidate {
	out := []comboCandidate{judge}
	for _, candidate := range candidates {
		if candidate != judge {
			out = append(out, candidate)
		}
	}
	return out
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
	alias := requestedAlias(req.ExecutorRequest, body)
	strategy := strategyFor(cfg, alias)
	call := func(ctx context.Context, candidate comboCandidate, request []byte, callback string) (pluginapi.HostModelExecutionResponse, error) {
		return executeHostModelWithHost(ctx, req, candidate, request, callback, hostCall)
	}
	selectorCall := func(ctx context.Context, candidate comboCandidate, request []byte, callback string) (pluginapi.HostModelExecutionResponse, error) {
		return executeHostModelWithHost(ctx, req, candidate, request, callback, hostCall)
	}
	resp, err := executeWithStrategy(context.Background(), alias, strategy, candidates, cfg, body, req.HostCallbackID, call, selectorCall)
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
	body = candidateRequestBody(candidate, body)
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
	alias := requestedAlias(req.ExecutorRequest, body)
	strategy := strategyFor(cfg, alias)
	candidates, strategy, rotationAlias, body, visionRouted, err := requestCandidatePool(alias, strategy, candidates, body)
	if err != nil {
		status, _ := AsStatusError(err)
		return errorEnvelope("executor_error", err.Error(), status.Code), nil
	}
	var pin comboTurnPin
	if strategy.Type == "round-robin" {
		pin = roundRobinPin(rotationAlias, strategy, body)
		candidates = roundRobinOrder(rotationAlias, strategy, candidates, pin)
	}
	requestID := newRequestCorrelationID()
	if visionRouted {
		logComboEvent("vision_route", requestID, alias, fmt.Sprintf(" strategy=%q candidate_count=%d streaming=true", strategy.Type, len(candidates)))
	}
	if strategy.Type == "smart" {
		pin = newComboTurnPin(rotationAlias, body)
		if pinned, ok := pin.pinned(candidates); ok {
			logComboEvent("smart_selection", requestID, alias, fmt.Sprintf(" outcome=sticky model=%q provider=%q streaming=true", pinned.Model, pinned.Provider))
			candidates = prioritizeCandidate(candidates, pinned)
		} else if selected, ok := selectSmartCandidate(context.Background(), requestID, alias, strategy, candidates, body, req.HostCallbackID, func(ctx context.Context, candidate comboCandidate, request []byte, callback string) (pluginapi.HostModelExecutionResponse, error) {
			return executeHostModelWithHost(ctx, req, candidate, request, callback, hostCall)
		}); ok {
			candidates = prioritizeCandidate(candidates, selected)
		} else {
			logComboEvent("smart_fallback", requestID, alias, " reason=selector_not_confident")
		}
	}
	for _, candidate := range candidates {
		if _, err := rewriteRequestModel(body, candidate.Model); err != nil {
			return errorEnvelope("executor_error", err.Error()), nil
		}
	}
	go func() {
		emit := func(b []byte) error { return emitPluginStreamChunkWithHost(req.StreamID, b, hostCall) }
		run := func(ctx context.Context, candidates []comboCandidate, request []byte) error {
			return executeStreamFallback(ctx, candidates, cfg, func(ctx context.Context, c comboCandidate, emit func([]byte) error) error {
				err := executeCandidateStreamWithHost(ctx, req, c, request, req.HostCallbackID, emit, hostCall)
				if err == nil {
					pin.remember(c)
				}
				return err
			}, emit)
		}
		runSmart := func(ctx context.Context, candidates []comboCandidate, request []byte) error {
			return executeStreamFallback(ctx, candidates, cfg, func(ctx context.Context, candidate comboCandidate, emit func([]byte) error) error {
				start := time.Now()
				logComboEvent("smart_candidate_start", requestID, alias, fmt.Sprintf(" model=%q provider=%q streaming=true", candidate.Model, candidate.Provider))
				err := executeCandidateStreamWithHost(ctx, req, candidate, request, req.HostCallbackID, emit, hostCall)
				status, _ := AsStatusError(err)
				if err == nil {
					status.Code = http.StatusOK
					pin.remember(candidate)
				}
				logComboEvent("smart_candidate_result", requestID, alias, fmt.Sprintf(" model=%q provider=%q outcome=%s status_code=%d error_kind=%q elapsed_ms=%d", candidate.Model, candidate.Provider, outcomeForError(err), status.Code, safeErrorKind(err), time.Since(start).Milliseconds()))
				return err
			}, emit)
		}
		var streamErr error
		if strategy.Type == "fusion" {
			streamErr = executeFusionStream(context.Background(), req, requestID, alias, strategy, candidates, cfg, body, hostCall, emit)
		} else if strategy.Type == "smart" {
			streamErr = runSmart(context.Background(), candidates, body)
		} else {
			streamErr = run(context.Background(), candidates, body)
		}
		closePluginStreamWithHost(req.StreamID, streamErr, hostCall)
	}()
	return okEnvelope(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
}

func executeCandidateStream(ctx context.Context, req rpcExecutorRequest, candidate comboCandidate, body []byte, callbackID string, emit func([]byte) error) error {
	return executeCandidateStreamWithHost(ctx, req, candidate, body, callbackID, emit, callHost)
}

func executeCandidateStreamWithHost(ctx context.Context, req rpcExecutorRequest, candidate comboCandidate, body []byte, callbackID string, emit func([]byte) error, hostCall rpcHostCall) error {
	body = candidateRequestBody(candidate, body)
	request, err := rewriteRequestModel(body, candidate.Model)
	if err != nil {
		return err
	}
	raw, err := hostCall(pluginabi.MethodHostModelExecuteStream, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{EntryProtocol: req.SourceFormat, ExitProtocol: candidateProtocol(candidate, req.SourceFormat), Model: candidate.Model, ForcedProvider: candidate.Provider, Stream: true, Body: request, Headers: req.Headers, Query: req.Query, Alt: req.Alt, CallerScope: "plugin-executor"},
		HostCallbackID:            callbackID,
	})
	if err != nil {
		return decodeStatusError(err)
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
	closeStream := sync.OnceFunc(func() {
		_, _ = hostCall(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: stream.StreamID})
	})
	defer closeStream()
	// Closing unblocks a pending read and cancels the upstream call.
	defer context.AfterFunc(ctx, closeStream)()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			// A close from cancellation also reports Done; do not mistake it for success.
			return ctx.Err()
		}
	}
}

func executeStreamFallback(ctx context.Context, candidates []comboCandidate, cfg comboConfig, run candidateStreamCall, emit func([]byte) error) error {
	if len(candidates) == 0 {
		return fmt.Errorf("combo has no candidates")
	}
	attempts := attemptsPerModel(cfg)
	firstByteLimit := time.Duration(cfg.FirstByteTimeoutSeconds) * time.Second
	var lastErr error
	for index, candidate := range candidates {
	tries:
		for attempt := 1; attempt <= attempts; attempt++ {
			if attempt > 1 {
				if err := waitBeforeRetry(ctx); err != nil {
					return err
				}
			} else if err := ctx.Err(); err != nil {
				return err
			}
			var emitted atomic.Bool
			guarded := func(chunk []byte) error {
				if len(chunk) == 0 {
					return nil
				}
				emitted.Store(true)
				return emit(chunk)
			}
			finalAttempt := index == len(candidates)-1 && attempt == attempts
			peek := &streamOutputPeek{emit: guarded}
			var err error
			if firstByteLimit > 0 && !finalAttempt {
				err = runWithFirstByteLimit(ctx, firstByteLimit, candidate, run, peek.write)
			} else {
				err = run(ctx, candidate, peek.write)
			}
			if err == nil {
				err = peek.finish()
			}
			if err == nil {
				return nil
			}
			lastErr = err
			// Output already reached the client, so no other attempt may write to it.
			if emitted.Load() || !shouldTryNextCandidate(err, cfg) {
				return err
			}
			if isContextOverflow(err) {
				break tries
			}
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
		// Without the detail the host reports every stream failure as a 500, which hides
		// rate limits and bad requests from the client.
		if status, ok := AsStatusError(err); ok {
			req.ErrorDetail = &pluginabi.Error{Code: "upstream_error", Message: err.Error(), HTTPStatus: status.Code}
		}
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
