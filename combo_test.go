package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func testConfig() comboConfig {
	return comboConfig{
		Enabled: true,
		ModelChains: map[string][]comboCandidate{
			"combo-coding": {
				{Provider: "codex", Model: "gpt-coder"},
				{Provider: "xai", Model: "grok-coder"},
			},
		},
		RetryStatuses: []int{403, 429, 502, 503, 504},
		MaxAttempts:   3,
	}
}

func TestCandidateProtocolUsesFallbackWhenUnset(t *testing.T) {
	if got := candidateProtocol(comboCandidate{}, "openai"); got != "openai" {
		t.Fatalf("candidate protocol = %q, want fallback protocol", got)
	}
	if got := candidateProtocol(comboCandidate{Protocol: "gemini"}, "openai"); got != "gemini" {
		t.Fatalf("candidate protocol = %q, want configured protocol", got)
	}
}

func TestValidateConfigAllowsCPAProviderResolution(t *testing.T) {
	cfg := comboConfig{
		Enabled: true,
		ModelChains: map[string][]comboCandidate{
			"combo-coding": {{Model: "example-model"}},
		},
		RetryStatuses: []int{403, 429, 502, 503, 504},
		MaxAttempts:   1,
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("validateConfig rejected a model resolved by CPA: %v", err)
	}
}

func TestExecuteFallbackAllowsCPAResolvedProvider(t *testing.T) {
	cfg := testConfig()
	candidates := []comboCandidate{{Model: "gemini-flash-latest"}}
	got, err := executeFallback(context.Background(), candidates, cfg, []byte(`{"model":"combo-coding"}`), "cb-test", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
		if candidate.Provider != "" {
			t.Fatalf("provider = %q, want CPA to resolve it from the model", candidate.Provider)
		}
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != `{"ok":true}` {
		t.Fatalf("response body = %s", got.Body)
	}
}

func TestPluginRegistrationAdvertisesModelRegistrarAndManagementAPI(t *testing.T) {
	got := pluginRegistration()
	if !got.Capabilities.ModelRegistrar {
		t.Fatal("combo plugin must register its aliases with CPA")
	}
	if !got.Capabilities.ManagementAPI {
		t.Fatal("combo plugin must expose its dashboard management page")
	}
	if got.Metadata.GitHubRepository != "https://github.com/ivanarifin/CPA-Combo-Plugin" {
		t.Fatalf("GitHubRepository = %q, want the public plugin repository", got.Metadata.GitHubRepository)
	}
	if len(got.Metadata.ConfigFields) != 0 {
		t.Fatalf("dashboard page should manage nested model chains, got generic config fields: %#v", got.Metadata.ConfigFields)
	}
	var decoded map[string]any
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	capabilities := decoded["capabilities"].(map[string]any)
	if capabilities["model_registrar"] != true {
		t.Fatalf("model_registrar capability = %v", capabilities["model_registrar"])
	}
	if capabilities["management_api"] != true {
		t.Fatalf("management_api capability = %v", capabilities["management_api"])
	}

}

func TestManagementRegistrationRPCAdvertisesResourceCapability(t *testing.T) {
	var decoded struct {
		Capabilities struct {
			ManagementAPI bool `json:"management_api"`
		} `json:"capabilities"`
	}
	raw, err := json.Marshal(pluginRegistration())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Capabilities.ManagementAPI {
		t.Fatal("management_api was not advertised on the RPC registration wire")
	}
}

func TestHandleMethodRegistersManagementRoutesAndServesEditor(t *testing.T) {
	registered, err := handleMethod(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var registrationEnvelope envelope
	if err := json.Unmarshal(registered, &registrationEnvelope); err != nil || !registrationEnvelope.OK {
		t.Fatalf("management registration=%s err=%v", registered, err)
	}
	var registration pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(registrationEnvelope.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Resources) != 1 || registration.Resources[0].Path != "/editor" {
		t.Fatalf("management resources = %#v", registration.Resources)
	}
	request, err := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/resource/plugins/cpa-combo-router/editor"})
	if err != nil {
		t.Fatal(err)
	}
	responseRaw, err := handleMethod(pluginabi.MethodManagementHandle, request)
	if err != nil {
		t.Fatal(err)
	}
	var responseEnvelope envelope
	if err := json.Unmarshal(responseRaw, &responseEnvelope); err != nil || !responseEnvelope.OK {
		t.Fatalf("management response=%s err=%v", responseRaw, err)
	}
	var response pluginapi.ManagementResponse
	if err := json.Unmarshal(responseEnvelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(response.Body), "Combo Router") {
		t.Fatalf("management editor response status=%d body contains page=%v", response.StatusCode, strings.Contains(string(response.Body), "Combo Router"))
	}
}

func TestRegisterManagementExposesComboEditorResource(t *testing.T) {
	raw, err := registerManagement()
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var result pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !got.OK || len(result.Resources) != 1 {
		t.Fatalf("management registration = %#v", result)
	}
	resource := result.Resources[0]
	if resource.Path != "/editor" || resource.Menu != "Combo Router" || resource.Description == "" {
		t.Fatalf("editor resource = %#v", resource)
	}
}

func TestManagementHandleServesSecureDashboardAndRejectsOtherRoutes(t *testing.T) {
	request, err := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/resource/plugins/cpa-combo-router/editor"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := handleManagement(request)
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var response pluginapi.ManagementResponse
	if err := json.Unmarshal(got.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !got.OK || response.StatusCode != http.StatusOK || response.Headers.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("dashboard response = %#v", response)
	}
	for _, expected := range []string{"New combo alias", "Add candidate model", "Save combo settings", "Choose model ID", "Search or type a model ID…", "Search or type a selector model ID…", "function modelID(value)", "model.toLowerCase()==='undefined'", "candidate.model=modelID(candidate.model)", "makeModelPicker(candidate)", "makeModelPicker(strategy.selector,'Selector model ID'", "grid.append(selector,provider.wrap,threshold.wrap)", "createElement('datalist')", "availableModels", "Routing strategy", "round-robin", "Smart selection", "Fusion + judge", "Minimum confidence", "Requests starting at each model", "strategies:savedStrategies", "strategies:normalizeStrategies(strategies,chains)", "delete strategies[selected]", "strategyLabel", "candidateHint", "Fusion panel models", "Image-triggered vision routing", "Route image requests to vision models", "strategy.vision_candidates", "Vision judge model ID (optional)", "refreshModelsBtn", "clientKeyForm", "clientAPIKey", "CPA client API key", "Forget key", "sessionStorage.setItem(CLIENT_KEY_STORAGE,value)", "base+'/v1/models'", "'Authorization':'Bearer '+clientAPIKey", "payload?.data", "owned_by", "id=\"saveFooter\"", "updatePendingChanges()", "retry_statuses", "/v0/management/plugins/", "/config", pluginID} {
		if !strings.Contains(string(response.Body), expected) {
			t.Errorf("dashboard is missing %q", expected)
		}
	}
	html := string(response.Body)
	if got := strings.Count(html, "base+'/v1/models'"); got != 1 {
		t.Errorf("dashboard aggregate model endpoint count = %d, want exactly 1", got)
	}
	if strings.Contains(html, "clientKeyGate") || strings.Contains(html, "role=\"dialog\" aria-modal=\"true\" aria-labelledby=\"clientKeyTitle\"") {
		t.Error("client API key entry must remain inline, not in a modal")
	}
	if strings.Contains(html, "/v0/management/auth-files") || strings.Contains(html, "/v0/management/auth-files/models") {
		t.Error("dashboard must not fetch models per auth file")
	}
	if response.Headers.Get("Content-Security-Policy") == "" || response.Headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers = %#v", response.Headers)
	}
	if strings.Contains(strings.ToLower(string(response.Body)), "pm2") {
		t.Fatal("dashboard copy must not assume a specific process manager")
	}
	badRequest, err := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: "/editor"})
	if err != nil {
		t.Fatal(err)
	}
	badRaw, err := handleManagement(badRequest)
	if err != nil {
		t.Fatal(err)
	}
	var badEnvelope envelope
	if err := json.Unmarshal(badRaw, &badEnvelope); err != nil {
		t.Fatal(err)
	}
	var badResponse pluginapi.ManagementResponse
	if err := json.Unmarshal(badEnvelope.Result, &badResponse); err != nil {
		t.Fatal(err)
	}
	if badResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("unsupported dashboard request status = %d, want 404", badResponse.StatusCode)
	}
}

func TestRegisterModelsReturnsConfiguredComboAliases(t *testing.T) {
	cfg := testConfig()
	currentConfig.Store(cfg)
	raw, err := registerModels()
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("registerModels failed: %s", raw)
	}
	var result pluginapi.ModelRegistrationResponse
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider != executorProviderID || len(result.Models) != 1 || result.Models[0].ID != "combo-coding" {
		t.Fatalf("model registration = %#v", result)
	}
}

func TestRegisterModelsOmitsAliasesWhenDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	currentConfig.Store(cfg)
	raw, err := registerModels()
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var result pluginapi.ModelRegistrationResponse
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 0 {
		t.Fatalf("disabled plugin registered models: %#v", result.Models)
	}
}

func TestRouteModelOnlyHandlesConfiguredCombo(t *testing.T) {
	cfg := testConfig()
	req := pluginapi.ModelRouteRequest{RequestedModel: "combo-coding", SourceFormat: "openai"}
	got := routeDecision(cfg, req)
	if !got.Handled || got.TargetKind != pluginapi.ModelRouteTargetExecutor || got.Target != pluginID {
		t.Fatalf("configured combo route = %#v", got)
	}
	req.RequestedModel = "gpt-coder"
	if got := routeDecision(cfg, req); got.Handled {
		t.Fatalf("ordinary model unexpectedly handled: %#v", got)
	}
}

func TestRewriteRequestModelPreservesOtherFields(t *testing.T) {
	in := []byte(`{"model":"combo-coding","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	got, err := rewriteRequestModel(in, "gpt-coder")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"messages":[{"role":"user","content":"hello"}],"model":"gpt-coder","stream":true}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rewritten body = %s, want %s", got, want)
	}
}

func TestRewriteRequestModelAddsModelForModelFreeJSON(t *testing.T) {
	in := []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`)
	got, err := rewriteRequestModel(in, "gemini-model")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "gemini-model" {
		t.Fatalf("model = %v, want injected target", payload["model"])
	}
}

func TestExecuteFallbackRetriesRetryableStatusAndReturnsSuccess(t *testing.T) {
	cfg := testConfig()
	var tried []string
	call := func(_ context.Context, c comboCandidate, _ []byte) (pluginapi.HostModelExecutionResponse, error) {
		tried = append(tried, c.Provider)
		if c.Provider == "codex" {
			return pluginapi.HostModelExecutionResponse{}, StatusError{Code: http.StatusTooManyRequests}
		}
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
	}
	got, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb-test", func(ctx context.Context, candidate comboCandidate, body []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
		return call(ctx, candidate, body)
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != `{"ok":true}` {
		t.Fatalf("response body = %s", got.Body)
	}
	if !reflect.DeepEqual(tried, []string{"codex", "xai"}) {
		t.Fatalf("attempt order = %v", tried)
	}
}

func TestExecuteFallbackDoesNotRetryClientError(t *testing.T) {
	cfg := testConfig()
	attempts := 0
	_, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb-test", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
		attempts++
		return pluginapi.HostModelExecutionResponse{}, StatusError{Code: http.StatusBadRequest}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("err=%v attempts=%d, want non-retryable error after one attempt", err, attempts)
	}
}

func TestExecuteFallbackHonorsAttemptLimit(t *testing.T) {
	cfg := testConfig()
	cfg.MaxAttempts = 1
	attempts := 0
	_, _ = executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb-test", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
		attempts++
		return pluginapi.HostModelExecutionResponse{}, StatusError{Code: http.StatusServiceUnavailable}
	})
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1", attempts)
	}
}

func TestStreamFallbackRetriesOnlyBeforeAnyPayloadIsEmitted(t *testing.T) {
	cfg := testConfig()
	var attempts int
	var output []string
	err := executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(_ context.Context, c comboCandidate, emit func([]byte) error) error {
		attempts++
		if c.Provider == "codex" {
			return StatusError{Code: http.StatusServiceUnavailable}
		}
		return emit([]byte("data: ok\n\n"))
	}, func(p []byte) error { output = append(output, string(p)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !reflect.DeepEqual(output, []string{"data: ok\n\n"}) {
		t.Fatalf("attempts=%d output=%v", attempts, output)
	}
}

func TestStreamFallbackNeverRetriesAfterDownstreamPayload(t *testing.T) {
	cfg := testConfig()
	var attempts int
	var output []string
	err := executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(_ context.Context, _ comboCandidate, emit func([]byte) error) error {
		attempts++
		if err := emit([]byte("data: partial\n\n")); err != nil {
			return err
		}
		return StatusError{Code: http.StatusServiceUnavailable}
	}, func(p []byte) error { output = append(output, string(p)); return nil })
	if err == nil || attempts != 1 || !reflect.DeepEqual(output, []string{"data: partial\n\n"}) {
		t.Fatalf("err=%v attempts=%d output=%v", err, attempts, output)
	}
}

func TestStreamFallbackDoesNotRetryUnknownErrors(t *testing.T) {
	cfg := testConfig()
	attempts := 0
	wantErr := errors.New("upstream protocol error")
	err := executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(context.Context, comboCandidate, func([]byte) error) error {
		attempts++
		return wantErr
	}, func([]byte) error { return nil })
	if !errors.Is(err, wantErr) || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestCandidatesUseExecutorModelAliasDespiteTranslatedPayload(t *testing.T) {
	req := pluginapi.ExecutorRequest{Model: "combo-coding", Payload: []byte(`{"model":"normalized"}`), OriginalRequest: []byte(`{"model":"combo-coding"}`)}
	got, _, err := candidatesFor(req, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Provider != "codex" {
		t.Fatalf("candidates=%#v", got)
	}
}
func TestStreamStatusErrorDoesNotGuessFromArbitraryNumbers(t *testing.T) {
	plainErr := StatusErrorFromMessage("upstream trace 429 retry")
	if _, ok := AsStatusError(plainErr); ok {
		t.Fatal("guessed status from text")
	}
	if plainErr.Error() != "upstream trace 429 retry" {
		t.Fatalf("lost upstream stream diagnostic: %v", plainErr)
	}
	if got, ok := AsStatusError(StatusErrorFromMessage(`{"status_code":429}`)); !ok || got.Code != 429 {
		t.Fatalf("got %#v ok=%v", got, ok)
	}
}
func TestDecodeStatusErrorEnvelope(t *testing.T) {
	err := decodeStatusError(errors.New(`{"code":"rate_limit_exceeded","message":"limited","http_status":429}`))
	got, ok := AsStatusError(err)
	if !ok || got.Code != 429 || got.Message != "limited" {
		t.Fatalf("got %#v ok=%v", got, ok)
	}
}

func TestValidateConfigBoundaries(t *testing.T) {
	valid := testConfig()
	tests := []struct {
		name    string
		mutate  func(*comboConfig)
		wantErr bool
	}{
		{name: "valid defaults", mutate: func(*comboConfig) {}},
		{name: "enabled without chains is a valid fresh install", mutate: func(c *comboConfig) { c.ModelChains = nil }},
		{name: "disabled without chains", mutate: func(c *comboConfig) { c.Enabled = false; c.ModelChains = nil }},
		{name: "blank alias", mutate: func(c *comboConfig) { c.ModelChains = map[string][]comboCandidate{" ": {{Model: "m"}}} }, wantErr: true},
		{name: "untrimmed alias", mutate: func(c *comboConfig) { c.ModelChains = map[string][]comboCandidate{" alias ": {{Model: "m"}}} }, wantErr: true},
		{name: "empty candidates", mutate: func(c *comboConfig) { c.ModelChains = map[string][]comboCandidate{"alias": nil} }, wantErr: true},
		{name: "blank model", mutate: func(c *comboConfig) { c.ModelChains = map[string][]comboCandidate{"alias": {{Model: "  "}}} }, wantErr: true},
		{name: "untrimmed model", mutate: func(c *comboConfig) { c.ModelChains = map[string][]comboCandidate{"alias": {{Model: " model "}}} }, wantErr: true},
		{name: "untrimmed provider", mutate: func(c *comboConfig) {
			c.ModelChains = map[string][]comboCandidate{"alias": {{Provider: " p ", Model: "m"}}}
		}, wantErr: true},
		{name: "attempts lower bound", mutate: func(c *comboConfig) { c.MaxAttempts = 0 }, wantErr: true},
		{name: "attempts upper bound", mutate: func(c *comboConfig) { c.MaxAttempts = 21 }, wantErr: true},
		{name: "retry 400 rejected", mutate: func(c *comboConfig) { c.RetryStatuses = []int{400} }, wantErr: true},
		{name: "retry 401 rejected", mutate: func(c *comboConfig) { c.RetryStatuses = []int{401} }, wantErr: true},
		{name: "retry 403 accepted", mutate: func(c *comboConfig) { c.RetryStatuses = []int{403} }},
		{name: "retry 499 rejected", mutate: func(c *comboConfig) { c.RetryStatuses = []int{499} }, wantErr: true},
		{name: "retry 600 rejected", mutate: func(c *comboConfig) { c.RetryStatuses = []int{600} }, wantErr: true},
		{name: "retry 429 accepted", mutate: func(c *comboConfig) { c.RetryStatuses = []int{429} }},
		{name: "retry 5xx bounds accepted", mutate: func(c *comboConfig) { c.RetryStatuses = []int{500, 599} }},
		{name: "20 attempts accepted", mutate: func(c *comboConfig) { c.MaxAttempts = 20 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			err := validateConfig(cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigureAllowsFreshStoreInstallWithoutChains(t *testing.T) {
	raw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte("enabled: true\npriority: 20\n")})
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(raw); err != nil {
		t.Fatalf("configure fresh store install: %v", err)
	}
	if cfg := loadedConfig(); !cfg.Enabled || len(cfg.ModelChains) != 0 {
		t.Fatalf("fresh install config = %#v", cfg)
	}
	registered, err := handleMethod(pluginabi.MethodPluginRegister, raw)
	if err != nil {
		t.Fatal(err)
	}
	var result envelope
	if err := json.Unmarshal(registered, &result); err != nil || !result.OK {
		t.Fatalf("fresh install registration = %s err=%v", registered, err)
	}
	modelsRaw, err := handleMethod(pluginabi.MethodModelRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var modelsEnvelope envelope
	if err := json.Unmarshal(modelsRaw, &modelsEnvelope); err != nil || !modelsEnvelope.OK {
		t.Fatalf("fresh install model registration = %s err=%v", modelsRaw, err)
	}
	var models pluginapi.ModelRegistrationResponse
	if err := json.Unmarshal(modelsEnvelope.Result, &models); err != nil || len(models.Models) != 0 {
		t.Fatalf("fresh install unexpectedly registered aliases: %#v err=%v", models, err)
	}
	if route := routeDecision(loadedConfig(), pluginapi.ModelRouteRequest{RequestedModel: "unconfigured-alias"}); route.Handled {
		t.Fatalf("fresh install routed an unconfigured model: %#v", route)
	}
}

func TestConfigureLoadsPerAliasStrategies(t *testing.T) {
	raw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte("enabled: true\nmodel_chains:\n  combo:\n    - model: panel-a\n    - model: panel-b\nstrategies:\n  combo:\n    type: fusion\n    judge:\n      provider: judge-provider\n      model: judge-model\n      protocol: openai\n    vision_enabled: true\n    vision_candidates:\n      - provider: vision-provider\n        model: vision-panel\n    vision_judge:\n      provider: vision-judge-provider\n      model: vision-judge-model\n")})
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(raw); err != nil {
		t.Fatalf("configure strategy: %v", err)
	}
	strategy := loadedConfig().Strategies["combo"]
	if strategy.Type != "fusion" || strategy.Judge.Provider != "judge-provider" || strategy.Judge.Model != "judge-model" || strategy.Judge.Protocol != "openai" || !strategy.VisionEnabled || !reflect.DeepEqual(strategy.VisionCandidates, []comboCandidate{{Provider: "vision-provider", Model: "vision-panel"}}) || strategy.VisionJudge.Provider != "vision-judge-provider" || strategy.VisionJudge.Model != "vision-judge-model" {
		t.Fatalf("loaded strategy = %#v", strategy)
	}
}

func TestConfigureDefaultsAndIsTransactional(t *testing.T) {
	before := testConfig()
	currentConfig.Store(before)
	validRaw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte("enabled: true\nmodel_chains:\n  combo:\n    - model: m\n")})
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(validRaw); err != nil {
		t.Fatalf("configure defaults: %v", err)
	}
	loaded := loadedConfig()
	if loaded.MaxAttempts != 3 || !reflect.DeepEqual(loaded.RetryStatuses, []int{403, 429, 502, 503, 504}) {
		t.Fatalf("default config = %#v", loaded)
	}
	invalidRaw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte("enabled: true\nmax_attempts: 21\nmodel_chains:\n  combo:\n    - model: m\n")})
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(invalidRaw); err == nil {
		t.Fatal("configure accepted invalid config")
	}
	if !reflect.DeepEqual(loadedConfig(), loaded) {
		t.Fatalf("invalid reconfigure changed active config: %#v", loadedConfig())
	}
}

func TestRewriteRequestModelRejectsMalformedAndNonObjectJSON(t *testing.T) {
	for _, body := range []string{"", "{", "null", "[]", `"string"`, "42"} {
		t.Run(body, func(t *testing.T) {
			if _, err := rewriteRequestModel([]byte(body), "target"); err == nil {
				t.Fatalf("rewriteRequestModel(%q) succeeded", body)
			}
		})
	}
}

func TestExecuteFallbackRetriesConfiguredStatusMatrix(t *testing.T) {
	for _, status := range []int{403, 429, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := testConfig()
			attempts := 0
			got, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
				attempts++
				if attempts == 1 {
					return pluginapi.HostModelExecutionResponse{}, StatusError{Code: status}
				}
				return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte("ok")}, nil
			})
			if err != nil || string(got.Body) != "ok" || attempts != 2 {
				t.Fatalf("response=%#v err=%v attempts=%d", got, err, attempts)
			}
		})
	}
}

func TestExecuteFallbackRetriesRetryableResponseStatus(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := testConfig()
			attempts := 0
			_, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
				attempts++
				if attempts == 1 {
					return pluginapi.HostModelExecutionResponse{StatusCode: status}, nil
				}
				return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK}, nil
			})
			if err != nil || attempts != 2 {
				t.Fatalf("err=%v attempts=%d", err, attempts)
			}
		})
	}
}

func TestExecuteFallbackDoesNotRetryUnlistedResponseStatusOrUnknownError(t *testing.T) {
	cases := []struct {
		name string
		call hostModelCall
	}{
		{name: "unauthorized response", call: func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusUnauthorized}, nil
		}},
		{name: "unlisted server response", call: func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusInternalServerError}, nil
		}},
		{name: "redirect response", call: func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusFound}, nil
		}},
		{name: "missing HTTP status", call: func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{Body: []byte("looks successful")}, nil
		}},
		{name: "unknown error", call: func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{}, errors.New("transport unavailable")
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.RetryStatuses = []int{429}
			attempts := 0
			_, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb", func(ctx context.Context, c comboCandidate, b []byte, id string) (pluginapi.HostModelExecutionResponse, error) {
				attempts++
				return tt.call(ctx, c, b, id)
			})
			if err == nil || attempts != 1 {
				t.Fatalf("err=%v attempts=%d, want fail without retry", err, attempts)
			}
		})
	}
}

func TestExecuteFallbackExhaustionReturnsLastRetryableError(t *testing.T) {
	cfg := testConfig()
	attempts := 0
	want := StatusError{Code: http.StatusServiceUnavailable, Message: "last candidate"}
	_, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
		attempts++
		return pluginapi.HostModelExecutionResponse{}, want
	})
	got, ok := AsStatusError(err)
	if !ok || got.Code != want.Code || attempts != 2 {
		t.Fatalf("err=%v status=%#v attempts=%d", err, got, attempts)
	}
}

func TestExecuteFallbackStopsBeforeCallingWhenContextCanceled(t *testing.T) {
	cfg := testConfig()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := executeFallback(ctx, cfg.ModelChains["combo-coding"], cfg, []byte(`{"model":"combo-coding"}`), "cb", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
		called = true
		return pluginapi.HostModelExecutionResponse{}, nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestCandidatesForUsesRequestedModelMetadataAndRejectsInvalidRequests(t *testing.T) {
	cfg := testConfig()
	req := pluginapi.ExecutorRequest{Model: "wrong-alias", Payload: []byte(`{"model":"wrong-alias"}`), Metadata: map[string]any{coreexecutor.RequestedModelMetadataKey: " combo-coding "}}
	candidates, _, err := candidatesFor(req, cfg)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("metadata candidates=%#v err=%v", candidates, err)
	}
	for _, invalid := range []pluginapi.ExecutorRequest{
		{Model: "missing"},
		{OriginalRequest: []byte("{")},
	} {
		if _, _, err := candidatesFor(invalid, cfg); err == nil {
			t.Fatalf("candidatesFor(%#v) unexpectedly succeeded", invalid)
		}
	}
}

func TestHandleMethodConfigureRegisterRouteAndErrors(t *testing.T) {
	config := "enabled: true\nmodel_chains:\n  combo-a:\n    - model: target-a\nmax_attempts: 1\n"
	encoded, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte(config)})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := handleMethod("plugin.register", encoded)
	if err != nil {
		t.Fatal(err)
	}
	var regEnvelope envelope
	if err := json.Unmarshal(registered, &regEnvelope); err != nil || !regEnvelope.OK {
		t.Fatalf("registration=%s err=%v", registered, err)
	}
	models, err := handleMethod("model.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	var modelEnvelope envelope
	if err := json.Unmarshal(models, &modelEnvelope); err != nil || !modelEnvelope.OK {
		t.Fatalf("models=%s err=%v", models, err)
	}
	routeRequest, err := json.Marshal(rpcModelRouteRequest{ModelRouteRequest: pluginapi.ModelRouteRequest{RequestedModel: "combo-a", SourceFormat: "openai"}})
	if err != nil {
		t.Fatal(err)
	}
	routed, err := handleMethod("model.route", routeRequest)
	if err != nil {
		t.Fatal(err)
	}
	var routeEnvelope envelope
	if err := json.Unmarshal(routed, &routeEnvelope); err != nil || !routeEnvelope.OK {
		t.Fatalf("route=%s err=%v", routed, err)
	}
	var route pluginapi.ModelRouteResponse
	if err := json.Unmarshal(routeEnvelope.Result, &route); err != nil || !route.Handled {
		t.Fatalf("route result=%#v err=%v", route, err)
	}
	unknown, err := handleMethod("unknown", nil)
	if err != nil {
		t.Fatal(err)
	}
	var unknownEnvelope envelope
	if err := json.Unmarshal(unknown, &unknownEnvelope); err != nil || unknownEnvelope.OK || unknownEnvelope.Error == nil || unknownEnvelope.Error.Code != "unknown_method" {
		t.Fatalf("unknown method envelope=%s err=%v", unknown, err)
	}
}

func TestExecuteRPCHandlesMalformedRequestsAndDisabledPlugin(t *testing.T) {
	t.Run("malformed rpc", func(t *testing.T) {
		if _, err := executeWithHost([]byte("{"), func(string, any) (json.RawMessage, error) {
			t.Fatal("malformed request called host")
			return nil, nil
		}); err == nil {
			t.Fatal("malformed executor request succeeded")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		cfg := testConfig()
		cfg.Enabled = false
		currentConfig.Store(cfg)
		got, err := executeWithHost([]byte(`{}`), func(string, any) (json.RawMessage, error) {
			t.Fatal("disabled request called host")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var response envelope
		if err := json.Unmarshal(got, &response); err != nil || response.OK || response.Error == nil || response.Error.Code != "executor_disabled" {
			t.Fatalf("disabled result=%s err=%v", got, err)
		}
	})
	t.Run("bad request body", func(t *testing.T) {
		currentConfig.Store(testConfig())
		request, err := json.Marshal(rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding", OriginalRequest: []byte("null")}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := executeWithHost(request, func(string, any) (json.RawMessage, error) {
			t.Fatal("invalid payload called host")
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var response envelope
		if err := json.Unmarshal(got, &response); err != nil || response.OK || response.Error == nil || response.Error.Code != "executor_error" {
			t.Fatalf("invalid request result=%s err=%v", got, err)
		}
	})
}

func TestExecuteStreamRPCValidatesRequestBeforeStartingHost(t *testing.T) {
	cfg := testConfig()
	currentConfig.Store(cfg)
	tests := []struct {
		name string
		req  rpcExecutorRequest
	}{
		{name: "missing IDs", req: rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding"}}},
		{name: "invalid original request", req: rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding", OriginalRequest: []byte("{")}, StreamID: "stream", HostCallbackID: "callback"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatal(err)
			}
			got, err := executeStreamWithHost(raw, func(string, any) (json.RawMessage, error) {
				t.Fatal("invalid stream request called host")
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var response envelope
			if err := json.Unmarshal(got, &response); err != nil || response.OK || response.Error == nil || response.Error.Code != "executor_error" {
				t.Fatalf("stream validation result=%s err=%v", got, err)
			}
		})
	}
}

func TestExecuteRPCPerformsRealHostCallbackFallback(t *testing.T) {
	cfg := testConfig()
	currentConfig.Store(cfg)
	var tried []string
	rawRequest, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model: "combo-coding", SourceFormat: "openai", OriginalRequest: []byte(`{"model":"combo-coding","messages":[{"role":"user","content":"hi"}]}`),
		},
		HostCallbackID: "test-host-callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := executeWithHost(rawRequest, func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostModelExecute {
			t.Fatalf("host method = %q", method)
		}
		request, ok := payload.(hostModelExecutionRequest)
		if !ok {
			t.Fatalf("host payload type = %T", payload)
		}
		tried = append(tried, request.Model)
		if request.HostCallbackID != "test-host-callback" {
			t.Fatalf("callback id = %q", request.HostCallbackID)
		}
		var body map[string]any
		if err := json.Unmarshal(request.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != request.Model {
			t.Fatalf("candidate model %q mismatches host body: %s", request.Model, request.Body)
		}
		if request.Model == "gpt-coder" {
			return nil, errors.New(`{"code":"forbidden","message":"forced structured test 403","http_status":403}`)
		}
		return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"fallback worked"}}]}`)})
	})
	if err != nil {
		t.Fatal(err)
	}
	var responseEnvelope envelope
	if err := json.Unmarshal(response, &responseEnvelope); err != nil || !responseEnvelope.OK {
		t.Fatalf("execute response=%s err=%v", response, err)
	}
	var result pluginapi.ExecutorResponse
	if err := json.Unmarshal(responseEnvelope.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tried, []string{"gpt-coder", "grok-coder"}) {
		t.Fatalf("candidate order = %v", tried)
	}
	if string(result.Payload) != `{"choices":[{"message":{"content":"fallback worked"}}]}` {
		t.Fatalf("payload = %s", result.Payload)
	}
}

func TestExecuteHostModelParsesResponsesAndStructuredCallbackErrors(t *testing.T) {
	tests := []struct {
		name        string
		response    json.RawMessage
		callbackErr error
		wantStatus  int
		wantError   bool
	}{
		{name: "success", response: json.RawMessage(`{"status_code":200,"body":"b2s="}`)},
		{name: "retryable response status", response: json.RawMessage(`{"status_code":503}`), wantStatus: 503, wantError: true},
		{name: "redirect response status rejected", response: json.RawMessage(`{"status_code":302}`), wantStatus: 302, wantError: true},
		{name: "missing response status rejected", response: json.RawMessage(`{"body":"b2s="}`), wantError: true},
		{name: "invalid response JSON", response: json.RawMessage(`{`), wantError: true},
		{name: "structured callback error", callbackErr: errors.New(`{"code":"overloaded","message":"busy","http_status":429}`), wantStatus: 429, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := executeHostModelWithHost(context.Background(), rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{SourceFormat: "openai"}}, comboCandidate{Model: "candidate"}, []byte(`{"model":"candidate"}`), "callback", func(method string, _ any) (json.RawMessage, error) {
				if method != pluginabi.MethodHostModelExecute {
					t.Fatalf("host method = %q", method)
				}
				return tt.response, tt.callbackErr
			})
			if (err != nil) != tt.wantError {
				t.Fatalf("response=%#v err=%v", got, err)
			}
			if tt.wantError {
				status, ok := AsStatusError(err)
				if tt.wantStatus > 0 && (!ok || status.Code != tt.wantStatus) {
					t.Fatalf("status=%#v ok=%v err=%v", status, ok, err)
				}
				return
			}
			if got.StatusCode != 200 || string(got.Body) != "ok" {
				t.Fatalf("response = %#v", got)
			}
		})
	}
}

func TestExecuteCandidateStreamClosesOrPreservesFailures(t *testing.T) {
	tests := []struct {
		name          string
		startResponse json.RawMessage
		startErr      error
		readResponse  json.RawMessage
		readErr       error
		wantStatus    int
		wantClosed    bool
		wantError     bool
	}{
		{name: "retryable status and close returned stream", startResponse: json.RawMessage(`{"status_code":503,"stream_id":"failed"}`), wantStatus: 503, wantClosed: true, wantError: true},
		{name: "status without stream id", startResponse: json.RawMessage(`{"status_code":503}`), wantStatus: 503, wantError: true},
		{name: "redirect status rejected", startResponse: json.RawMessage(`{"status_code":302,"stream_id":"redirected"}`), wantStatus: 302, wantClosed: true, wantError: true},
		{name: "missing status rejected", startResponse: json.RawMessage(`{"stream_id":"missing-status"}`), wantClosed: true, wantError: true},
		{name: "malformed start response", startResponse: json.RawMessage(`{`), wantError: true},
		{name: "empty stream id", startResponse: json.RawMessage(`{"status_code":200}`), wantError: true},
		{name: "stream read callback error", startResponse: json.RawMessage(`{"status_code":200,"stream_id":"s"}`), readErr: errors.New("read failed"), wantClosed: true, wantError: true},
		{name: "malformed read response", startResponse: json.RawMessage(`{"status_code":200,"stream_id":"s"}`), readResponse: json.RawMessage(`{`), wantClosed: true, wantError: true},
		{name: "structured retryable read error", startResponse: json.RawMessage(`{"status_code":200,"stream_id":"s"}`), readResponse: json.RawMessage(`{"error":"{\"status_code\":503}"}`), wantStatus: 503, wantClosed: true, wantError: true},
		{name: "done closes stream", startResponse: json.RawMessage(`{"status_code":200,"stream_id":"s"}`), readResponse: json.RawMessage(`{"done":true}`), wantClosed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closed := false
			calls := 0
			req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{SourceFormat: "openai"}}
			err := executeCandidateStreamWithHost(context.Background(), req, comboCandidate{Model: "target"}, []byte(`{"model":"alias"}`), "callback", func([]byte) error { return nil }, func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostModelExecuteStream:
					return tt.startResponse, tt.startErr
				case pluginabi.MethodHostModelStreamRead:
					calls++
					if tt.readErr != nil {
						return nil, tt.readErr
					}
					return tt.readResponse, nil
				case pluginabi.MethodHostModelStreamClose:
					closed = true
					if request, ok := payload.(pluginapi.HostModelStreamCloseRequest); !ok || request.StreamID == "" {
						t.Fatalf("close payload = %#v", payload)
					}
					return json.RawMessage(`{}`), nil
				default:
					t.Fatalf("unexpected host method %q", method)
					return nil, errors.New("unexpected host method")
				}
			})
			if (err != nil) != tt.wantError || closed != tt.wantClosed {
				t.Fatalf("err=%v closed=%v wantError=%v wantClosed=%v", err, closed, tt.wantError, tt.wantClosed)
			}
			if tt.wantStatus > 0 {
				status, ok := AsStatusError(err)
				if !ok || status.Code != tt.wantStatus {
					t.Fatalf("status=%#v ok=%v err=%v", status, ok, err)
				}
			}
			if tt.readErr != nil && calls != 1 {
				t.Fatalf("read calls=%d, want 1", calls)
			}
		})
	}
}

func TestExecuteStreamRPCFallbackOnRetryableStartStatusClosesFailedStream(t *testing.T) {
	cfg := testConfig()
	currentConfig.Store(cfg)
	var started []string
	var closedStreams []string
	var emitted []string
	closed := make(chan struct{})
	request, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding", SourceFormat: "openai", Stream: true, OriginalRequest: []byte(`{"model":"combo-coding"}`)},
		StreamID:        "downstream", HostCallbackID: "callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeStreamWithHost(request, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			hostReq := payload.(hostModelExecutionRequest)
			started = append(started, hostReq.Model)
			if hostReq.Model == "gpt-coder" {
				return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: http.StatusServiceUnavailable, StreamID: "failed-upstream"})
			}
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: http.StatusOK, StreamID: "working-upstream"})
		case pluginabi.MethodHostModelStreamClose:
			closedStreams = append(closedStreams, payload.(pluginapi.HostModelStreamCloseRequest).StreamID)
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostModelStreamRead:
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte("data: recovered\\n\\n"), Done: true})
		case pluginabi.MethodHostStreamEmit:
			emitted = append(emitted, string(payload.(rpcStreamEmitRequest).Payload))
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			close(closed)
			return json.Marshal(map[string]any{})
		default:
			t.Fatalf("unexpected host method %q", method)
			return nil, errors.New("unexpected host method")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for downstream stream close")
	}
	if !reflect.DeepEqual(started, []string{"gpt-coder", "grok-coder"}) {
		t.Fatalf("stream candidates = %v", started)
	}
	if !reflect.DeepEqual(closedStreams, []string{"failed-upstream", "working-upstream"}) {
		t.Fatalf("closed upstream stream IDs = %v", closedStreams)
	}
	if !reflect.DeepEqual(emitted, []string{"data: recovered\\n\\n"}) {
		t.Fatalf("emitted chunks = %q", emitted)
	}
}

func TestFusionStreamToolRequestUsesJudgeStream(t *testing.T) {
	cfg := testConfig()
	cfg.Strategies = map[string]comboStrategy{"combo-coding": {Type: "fusion", Judge: comboCandidate{Model: "judge-model"}}}
	currentConfig.Store(cfg)
	var panelCalls []string
	var streamCalls []string
	var emitted []string
	var judgeRequest []byte
	var callsMu sync.Mutex
	closed := make(chan struct{})
	request, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			Model: "combo-coding", SourceFormat: "openai", Stream: true,
			OriginalRequest: []byte(`{"model":"combo-coding","messages":[{"role":"user","content":"Inspect and edit the project."}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],"tool_choice":"auto","stream":true}`),
		},
		StreamID: "fusion-agent-stream", HostCallbackID: "callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := executeStreamWithHost(request, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecute:
			hostReq := payload.(hostModelExecutionRequest)
			callsMu.Lock()
			panelCalls = append(panelCalls, hostReq.Model)
			callsMu.Unlock()
			var body map[string]any
			if err := json.Unmarshal(hostReq.Body, &body); err != nil {
				return nil, err
			}
			if body["model"] != hostReq.Model || body["tools"] == nil || body["tool_choice"] != "auto" || body["stream"] != false || hostReq.Stream {
				return nil, fmt.Errorf("fusion panel tool request altered: body=%s stream=%v", hostReq.Body, hostReq.Stream)
			}
			if hostReq.Model == "gpt-coder" {
				return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"Need read source","tool_calls":[{"id":"panel-call","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"src/main.go\"}"}}]}}]}`)})
			}
			return json.Marshal(pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"Read the requested source file."}}]}`)})
		case pluginabi.MethodHostModelExecuteStream:
			hostReq := payload.(hostModelExecutionRequest)
			callsMu.Lock()
			streamCalls = append(streamCalls, hostReq.Model)
			judgeRequest = append([]byte(nil), hostReq.Body...)
			callsMu.Unlock()
			var body map[string]any
			if err := json.Unmarshal(hostReq.Body, &body); err != nil {
				return nil, err
			}
			messages, _ := body["messages"].([]any)
			messagesJSON, _ := json.Marshal(messages)
			messagesText := string(messagesJSON)
			if hostReq.Model != "judge-model" || !hostReq.Stream || body["tools"] == nil || body["stream"] != true || !strings.Contains(messagesText, "read_file") || !strings.Contains(messagesText, "panel-call") || !strings.Contains(messagesText, "tool_calls") {
				return nil, fmt.Errorf("judge request did not preserve tools and structured proposal: %s", hostReq.Body)
			}
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: http.StatusOK, StreamID: "upstream-agent-stream"})
		case pluginabi.MethodHostModelStreamRead:
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte("data: tool-call\n\n"), Done: true})
		case pluginabi.MethodHostModelStreamClose:
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamEmit:
			callsMu.Lock()
			emitted = append(emitted, string(payload.(rpcStreamEmitRequest).Payload))
			callsMu.Unlock()
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			close(closed)
			return json.Marshal(map[string]any{})
		default:
			return nil, fmt.Errorf("unexpected host method %q", method)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var responseEnvelope envelope
	if err := json.Unmarshal(response, &responseEnvelope); err != nil || !responseEnvelope.OK {
		t.Fatalf("stream start response=%s err=%v", response, err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fusion agent stream to close")
	}
	callsMu.Lock()
	panelCallsCopy := append([]string(nil), panelCalls...)
	streamCallsCopy := append([]string(nil), streamCalls...)
	judgeRequestCopy := append([]byte(nil), judgeRequest...)
	emittedCopy := append([]string(nil), emitted...)
	callsMu.Unlock()
	if len(panelCallsCopy) != 2 || !containsString(panelCallsCopy, "gpt-coder") || !containsString(panelCallsCopy, "grok-coder") || !reflect.DeepEqual(streamCallsCopy, []string{"judge-model"}) || len(judgeRequestCopy) == 0 || len(emittedCopy) != 1 || emittedCopy[0] != "data: tool-call\n\n" {
		t.Fatalf("panel calls=%v judge streams=%v judge body=%s emitted=%q", panelCallsCopy, streamCallsCopy, judgeRequestCopy, emittedCopy)
	}
}

func TestExecuteStreamRPCDoesNotFallbackAfterDownstreamOutput(t *testing.T) {
	cfg := testConfig()
	currentConfig.Store(cfg)
	var methods []string
	var started []string
	var emitted []string
	reads := 0
	closed := make(chan struct{})
	request, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding", SourceFormat: "openai", Stream: true, OriginalRequest: []byte(`{"model":"combo-coding"}`)},
		StreamID:        "downstream", HostCallbackID: "callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeStreamWithHost(request, func(method string, payload any) (json.RawMessage, error) {
		methods = append(methods, method)
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			started = append(started, payload.(hostModelExecutionRequest).Model)
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: http.StatusOK, StreamID: "stream-partial"})
		case pluginabi.MethodHostModelStreamRead:
			reads++
			if reads == 1 {
				return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte("data: partial\\n\\n"), Done: false})
			}
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Error: `{"status_code":503}`})
		case pluginabi.MethodHostStreamEmit:
			emitted = append(emitted, string(payload.(rpcStreamEmitRequest).Payload))
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostModelStreamClose:
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			close(closed)
			return json.Marshal(map[string]any{})
		default:
			t.Fatalf("unexpected host method %q", method)
			return nil, errors.New("unexpected host method")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream close")
	}
	if !reflect.DeepEqual(started, []string{"gpt-coder"}) {
		t.Fatalf("stream started on candidates %v after downstream output", started)
	}
	if !reflect.DeepEqual(emitted, []string{"data: partial\\n\\n"}) {
		t.Fatalf("emitted = %q", emitted)
	}
	if len(methods) != 6 || methods[len(methods)-1] != pluginabi.MethodHostStreamClose {
		t.Fatalf("host methods = %v", methods)
	}
}

func TestExecuteStreamRPCRetriesBeforeEmissionAndClosesStreams(t *testing.T) {
	cfg := testConfig()
	currentConfig.Store(cfg)
	var methods []string
	var tried []string
	var emitted []string
	var closeError string
	closed := make(chan struct{})
	request, err := json.Marshal(rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding", SourceFormat: "openai", Stream: true, OriginalRequest: []byte(`{"model":"combo-coding","messages":[{"role":"user","content":"hi"}]}`)},
		StreamID:        "downstream-stream", HostCallbackID: "test-host-callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := executeStreamWithHost(request, func(method string, payload any) (json.RawMessage, error) {
		methods = append(methods, method)
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			hostReq := payload.(hostModelExecutionRequest)
			tried = append(tried, hostReq.Model)
			if hostReq.Model == "gpt-coder" {
				return nil, StatusError{Code: http.StatusServiceUnavailable, Message: "forced test transient"}
			}
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: http.StatusOK, StreamID: "upstream-stream"})
		case pluginabi.MethodHostModelStreamRead:
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte("data: streamed\\n\\n"), Done: true})
		case pluginabi.MethodHostModelStreamClose:
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamEmit:
			chunk := payload.(rpcStreamEmitRequest)
			emitted = append(emitted, string(chunk.Payload))
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			closeError = payload.(rpcStreamCloseRequest).Error
			close(closed)
			return json.Marshal(map[string]any{})
		default:
			t.Fatalf("unexpected host method %q", method)
			return nil, errors.New("unexpected host method")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var responseEnvelope envelope
	if err := json.Unmarshal(response, &responseEnvelope); err != nil || !responseEnvelope.OK {
		t.Fatalf("stream start response=%s err=%v", response, err)
	}
	var start struct {
		Headers http.Header `json:"headers"`
	}
	if err := json.Unmarshal(responseEnvelope.Result, &start); err != nil {
		t.Fatal(err)
	}
	if start.Headers.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream headers = %#v", start.Headers)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for downstream stream close")
	}
	if closeError != "" {
		t.Fatalf("downstream stream close reported error: %s", closeError)
	}
	if !reflect.DeepEqual(tried, []string{"gpt-coder", "grok-coder"}) {
		t.Fatalf("stream candidate order = %v", tried)
	}
	if !reflect.DeepEqual(emitted, []string{"data: streamed\\n\\n"}) {
		t.Fatalf("emitted chunks = %q", emitted)
	}
	if !reflect.DeepEqual(methods, []string{
		pluginabi.MethodHostModelExecuteStream,
		pluginabi.MethodHostModelExecuteStream,
		pluginabi.MethodHostModelStreamRead,
		pluginabi.MethodHostStreamEmit,
		pluginabi.MethodHostModelStreamClose,
		pluginabi.MethodHostStreamClose,
	}) {
		t.Fatalf("host callback sequence = %v", methods)
	}
}

func TestExecuteFallbackPassesCandidateAndRewrittenBodyToHost(t *testing.T) {
	cfg := testConfig()
	cfg.ModelChains["combo-coding"][0].Protocol = "openai-response"
	_, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"][:1], cfg, []byte(`{"model":"combo-coding","input":"hello"}`), "callback-123", func(_ context.Context, candidate comboCandidate, body []byte, callback string) (pluginapi.HostModelExecutionResponse, error) {
		if candidate.Model != "gpt-coder" || candidate.Provider != "codex" || candidateProtocol(candidate, "openai") != "openai-response" || callback != "callback-123" {
			t.Fatalf("candidate=%#v callback=%q", candidate, callback)
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got["model"] != "gpt-coder" || got["input"] != "hello" {
			t.Fatalf("request body=%s", body)
		}
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStreamFallbackEmptyChunksDoNotBlockRetry(t *testing.T) {
	cfg := testConfig()
	attempts := 0
	var output []string
	err := executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(_ context.Context, _ comboCandidate, emit func([]byte) error) error {
		attempts++
		if err := emit(nil); err != nil {
			return err
		}
		if err := emit([]byte{}); err != nil {
			return err
		}
		if attempts == 1 {
			return StatusError{Code: http.StatusServiceUnavailable}
		}
		return emit([]byte("data: success\n\n"))
	}, func(chunk []byte) error { output = append(output, string(chunk)); return nil })
	if err != nil || attempts != 2 || !reflect.DeepEqual(output, []string{"data: success\n\n"}) {
		t.Fatalf("err=%v attempts=%d output=%v", err, attempts, output)
	}
}

func TestStreamFallbackExhaustionAndCallbackError(t *testing.T) {
	t.Run("exhaustion", func(t *testing.T) {
		cfg := testConfig()
		attempts := 0
		want := StatusError{Code: http.StatusBadGateway}
		err := executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(context.Context, comboCandidate, func([]byte) error) error {
			attempts++
			return want
		}, func([]byte) error { return nil })
		got, ok := AsStatusError(err)
		if !ok || got.Code != want.Code || attempts != 2 {
			t.Fatalf("err=%v status=%#v attempts=%d", err, got, attempts)
		}
	})
	t.Run("downstream callback error", func(t *testing.T) {
		cfg := testConfig()
		attempts := 0
		want := errors.New("downstream closed")
		err := executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(_ context.Context, _ comboCandidate, emit func([]byte) error) error {
			attempts++
			return emit([]byte("data: payload\\n\\n"))
		}, func([]byte) error { return want })
		if !errors.Is(err, want) || attempts != 1 {
			t.Fatalf("err=%v attempts=%d", err, attempts)
		}
	})
}

func TestStreamFallbackHonorsMaxAttemptsAndContext(t *testing.T) {
	cfg := testConfig()
	cfg.MaxAttempts = 1
	attempts := 0
	_ = executeStreamFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, func(context.Context, comboCandidate, func([]byte) error) error {
		attempts++
		return StatusError{Code: http.StatusServiceUnavailable}
	}, func([]byte) error { return nil })
	if attempts != 1 {
		t.Fatalf("attempts=%d, want 1", attempts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := executeStreamFallback(ctx, cfg.ModelChains["combo-coding"], cfg, func(context.Context, comboCandidate, func([]byte) error) error {
		called = true
		return nil
	}, func([]byte) error { return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestExecuteFallbackRejectsEmptyCandidatesAndInvalidBody(t *testing.T) {
	cfg := testConfig()
	if _, err := executeFallback(context.Background(), nil, cfg, []byte(`{}`), "cb", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
		return pluginapi.HostModelExecutionResponse{}, nil
	}); err == nil {
		t.Fatal("empty candidate chain succeeded")
	}
	calls := 0
	if _, err := executeFallback(context.Background(), cfg.ModelChains["combo-coding"], cfg, []byte("null"), "cb", func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
		calls++
		return pluginapi.HostModelExecutionResponse{}, nil
	}); err == nil || calls != 0 {
		t.Fatalf("err=%v calls=%d, want validation failure before host call", err, calls)
	}
}

func TestResponseTextExtractsResponsesAPIOutput(t *testing.T) {
	body := []byte(`{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"private reasoning"}],"content":[{"type":"reasoning_text","text":"private reasoning"}]},{"type":"message","content":[{"type":"output_text","text":"first"},{"type":"output_text","text":"second"}]}]}`)
	if got := responseText(body); got != "first\nsecond" {
		t.Fatalf("response text = %q", got)
	}
	if got := latestUserText([]byte(`{"input":"responses api user prompt"}`)); got != "responses api user prompt" {
		t.Fatalf("latest user text = %q", got)
	}
}

func TestRoundRobinRotatesStartingCandidate(t *testing.T) {
	resetComboRotations()
	candidates := []comboCandidate{{Model: "a"}, {Model: "b"}, {Model: "c"}}
	var starts []string
	for range 6 {
		ordered := rotateCandidates("test-round-robin", candidates, 2)
		starts = append(starts, ordered[0].Model)
	}
	if want := []string{"a", "a", "b", "b", "c", "c"}; !reflect.DeepEqual(starts, want) {
		t.Fatalf("round-robin starts = %v, want %v", starts, want)
	}
}

func TestSmartSelectorTimeoutBudget(t *testing.T) {
	if smartSelectorTimeout != 60*time.Second {
		t.Fatalf("smart selector timeout=%s, want 60s", smartSelectorTimeout)
	}
}

func TestSmartSelectorBodyUsesCompactSelectionContext(t *testing.T) {
	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "chat messages", body: `{"messages":[{"role":"system","content":"old system context"},{"role":"user","content":"old user turn"},{"role":"assistant","content":"old assistant turn"},{"role":"user","content":"latest user request"}],"tools":[{}],"stream":true}`, key: "messages"},
		{name: "Responses input list", body: `{"input":[{"type":"message","role":"user","content":"latest Responses request"},{"type":"function_call_output","output":"private old tool result"}],"tools":[{}],"stream":true}`, key: "input"},
		{name: "Gemini contents", body: `{"contents":[{"role":"user","parts":[{"text":"latest Gemini request"}]}],"tools":[{}],"stream":true}`, key: "contents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := smartSelectorBody([]byte(tt.body), "Choose from model-a and model-b based on latest request: latest user request")
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(got, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["stream"] != false || payload["tools"] != nil || payload[tt.key] == nil {
				t.Fatalf("unexpected compact selector request: %s", got)
			}
			for _, omitted := range []string{"old system context", "old user turn", "old assistant turn", "private old tool result"} {
				if strings.Contains(string(got), omitted) {
					t.Fatalf("selector request retained %q: %s", omitted, got)
				}
			}
			if !strings.Contains(string(got), "model-a") || !strings.Contains(string(got), "latest user request") {
				t.Fatalf("selector request lost decision context: %s", got)
			}
		})
	}
}

func TestSmartSelectionPrioritizesChoiceAndFailsOpen(t *testing.T) {
	candidates := []comboCandidate{{Provider: "first", Model: "model-a"}, {Provider: "second", Model: "model-b"}}
	strategy := comboStrategy{Type: "smart", Selector: comboCandidate{Provider: "selector", Model: "selector-model"}, ConfidenceThreshold: 0.8}
	body := []byte(`{"model":"combo","messages":[{"role":"user","content":"write a test"}],"stream":true,"tools":[{}]}`)
	t.Run("selected model is attempted first", func(t *testing.T) {
		var calls []string
		_, err := executeWithStrategy(context.Background(), "smart-alias", strategy, candidates, testConfig(), body, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			calls = append(calls, candidate.Model)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
		}, func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			if candidate.Model != "selector-model" || candidate.Provider != "selector" {
				t.Fatalf("selector candidate = %#v", candidate)
			}
			var payload map[string]any
			if err := json.Unmarshal(request, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["stream"] != false || payload["tools"] != nil {
				t.Fatalf("selector request retained streaming/tool options: %s", request)
			}
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"{\"choice\":\"second/model-b\",\"confidence\":0.95}"}}]}`)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []string{"model-b"}) {
			t.Fatalf("candidate calls = %v", calls)
		}
	})
	t.Run("low confidence falls back to configured order", func(t *testing.T) {
		var calls []string
		_, err := executeWithStrategy(context.Background(), "smart-alias-low", strategy, candidates, testConfig(), body, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			calls = append(calls, candidate.Model)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{}`)}, nil
		}, func(context.Context, comboCandidate, []byte, string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"{\"choice\":\"second/model-b\",\"confidence\":0.5}"}}]}`)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []string{"model-a"}) {
			t.Fatalf("fallback calls = %v", calls)
		}
	})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func captureComboLogs(t *testing.T, run func()) string {
	t.Helper()
	previous := log.Writer()
	var output bytes.Buffer
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	run()
	return output.String()
}

func TestFusionLogsCorrelatedRedactedPanelAndJudgeEvents(t *testing.T) {
	requestID := "fusion-correlation-test"
	alias := "combo-private-alias"
	candidates := []comboCandidate{{Provider: "provider-a", Model: "panel-a"}, {Provider: "provider-b", Model: "panel-b"}}
	strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Provider: "judge-provider", Model: "judge-model"}}
	var promptBody []byte
	logs := captureComboLogs(t, func() {
		var err error
		promptBody, _, err = prepareFusionPromptObserved(context.Background(), requestID, alias, strategy, candidates, []byte(`{"model":"combo","messages":[{"role":"user","content":"private user prompt"}],"tools":[{"type":"function","function":{"name":"private_tool","parameters":{"type":"object"}}}]}`), "callback-secret", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			if candidate.Model == "panel-b" {
				return pluginapi.HostModelExecutionResponse{}, StatusError{Code: http.StatusServiceUnavailable, Message: "private raw upstream failure"}
			}
			panelResponse, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "private panel answer", "tool_calls": []any{map[string]any{"id": "private-call-id", "type": "function", "function": map[string]any{"name": "private_tool", "arguments": `{"secret":"private-argument"}`}}}}}}})
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: panelResponse}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		judge := strategy.Judge
		call := observedFusionCall(requestID, alias, judge, func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
		})
		if _, err := call(context.Background(), judge, promptBody, "callback-secret"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"fusion_panel_start", "fusion_panel_result", "fusion_panels_complete", "fusion_judge_start", "fusion_judge_result", requestID, "model=\"panel-a\"", "model=\"panel-b\"", "model=\"judge-model\"", "outcome=success", "status_code=503", "tool_call=true"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q: %s", want, logs)
		}
	}
	for _, secret := range []string{"private user prompt", "private panel answer", "private raw upstream failure", "private-argument", "private-call-id", "callback-secret", "private_tool"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs leaked %q: %s", secret, logs)
		}
	}
}

func TestSmartSelectionLogsSelectedCandidateWithoutPrompt(t *testing.T) {
	strategy := comboStrategy{Type: "smart", Selector: comboCandidate{Provider: "selector-provider", Model: "selector-model"}, ConfidenceThreshold: 0.8}
	candidates := []comboCandidate{{Provider: "provider-a", Model: "model-a"}, {Provider: "provider-b", Model: "model-b"}}
	selectionResponse, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"choice":"provider-b/model-b","confidence":0.93}`}}}})
	if selection, ok := parseSmartSelection(selectionResponse); !ok || selection.Choice != "provider-b/model-b" {
		t.Fatalf("smart selection fixture=%s parsed=%#v ok=%v", selectionResponse, selection, ok)
	}
	logs := captureComboLogs(t, func() {
		selected, ok := selectSmartCandidate(context.Background(), "smart-correlation-test", "smart-private-alias", strategy, candidates, []byte(`{"messages":[{"role":"user","content":"private selection prompt"}]}`), "callback-secret", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			if candidate.Model != "selector-model" {
				t.Fatalf("selector candidate=%#v", candidate)
			}
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: selectionResponse}, nil
		})
		if !ok || selected.Model != "model-b" {
			t.Fatalf("selection=%#v ok=%v", selected, ok)
		}
	})
	for _, want := range []string{"smart_selector_start", "smart_selector_result", "smart-correlation-test", "outcome=selected", "model=\"model-b\"", "confidence=0.930"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q: %s", want, logs)
		}
	}
	for _, secret := range []string{"private selection prompt", "callback-secret", "provider-b/model-b"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs leaked %q: %s", secret, logs)
		}
	}
}

func TestFusionRunsPanelsInParallelAndUsesJudge(t *testing.T) {
	cfg := testConfig()
	cfg.MaxAttempts = 2
	candidates := []comboCandidate{{Provider: "p1", Model: "panel-a"}, {Provider: "p2", Model: "panel-b"}}
	strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Provider: "judge-provider", Model: "judge-model"}}
	body := []byte(`{"model":"combo","messages":[{"role":"user","content":"solve this"}],"stream":true}`)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	var judgeRequest []byte
	result := make(chan error, 1)
	go func() {
		_, err := executeWithStrategy(context.Background(), "fusion-test", strategy, candidates, cfg, body, "cb", func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			if candidate.Model == "judge-model" {
				mu.Lock()
				judgeRequest = append([]byte(nil), request...)
				mu.Unlock()
				return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"judged":true}`)}, nil
			}
			var payload map[string]any
			if err := json.Unmarshal(request, &payload); err != nil {
				return pluginapi.HostModelExecutionResponse{}, err
			}
			if payload["stream"] != false || payload["tools"] != nil {
				return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("panel options not normalized: %s", request)
			}
			arrived <- struct{}{}
			select {
			case <-release:
			case <-time.After(2 * time.Second):
				return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("panel calls were not released")
			}
			content := "answer from " + candidate.Model
			response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: response}, nil
		}, nil)
		result <- err
	}()
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("both panel models were not called concurrently")
		}
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(judgeRequest) == 0 || !strings.Contains(string(judgeRequest), "answer from panel-a") || !strings.Contains(string(judgeRequest), "answer from panel-b") {
		t.Fatalf("judge request missing panel answers: %s", judgeRequest)
	}
	if strings.Contains(string(judgeRequest), `"tools"`) || !strings.Contains(string(judgeRequest), "If no tool call is needed, output only the final user-facing answer") {
		t.Fatalf("judge request retained tool definitions or lacks final-only instruction: %s", judgeRequest)
	}
}

func TestFusionToolEnabledRequestRunsPanelsAndJudge(t *testing.T) {
	strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Model: "judge-model"}}
	candidates := []comboCandidate{{Model: "panel-a"}, {Model: "panel-b"}}
	body := []byte(`{"model":"combo-coding","messages":[{"role":"user","content":"Inspect this project and implement the requested change."}],"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],"tool_choice":"auto","stream":true}`)
	var panelCalls, judgeCalls []string
	var callsMu sync.Mutex
	response, err := executeWithStrategy(context.Background(), "fusion-tools", strategy, candidates, testConfig(), body, "cb", func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
		var payload map[string]any
		if err := json.Unmarshal(request, &payload); err != nil {
			return pluginapi.HostModelExecutionResponse{}, err
		}
		if payload["model"] != candidate.Model || payload["tools"] == nil || payload["tool_choice"] != "auto" || payload["stream"] != false {
			return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("Fusion panel request lost tool options: %s", request)
		}
		if candidate.Model == "judge-model" {
			callsMu.Lock()
			judgeCalls = append(judgeCalls, candidate.Model)
			callsMu.Unlock()
			messages, _ := payload["messages"].([]any)
			if !strings.Contains(fmt.Sprint(messages), "read_file") || !strings.Contains(fmt.Sprint(messages), `"tool_calls"`) {
				return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("judge lost tool schema or panel proposal: %s", request)
			}
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"judge-call","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"src/main.go\"}"}}]},"finish_reason":"tool_calls"}]}`)}, nil
		}
		callsMu.Lock()
		panelCalls = append(panelCalls, candidate.Model)
		callsMu.Unlock()
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"Need inspect source","tool_calls":[{"id":"panel-call","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"src/main.go\"}"}}]}}]}`)}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	choices, _ := result["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	callsMu.Lock()
	panelCallsCopy := append([]string(nil), panelCalls...)
	judgeCallsCopy := append([]string(nil), judgeCalls...)
	callsMu.Unlock()
	if len(panelCallsCopy) != 2 || !containsString(panelCallsCopy, "panel-a") || !containsString(panelCallsCopy, "panel-b") || !reflect.DeepEqual(judgeCallsCopy, []string{"judge-model"}) || message["tool_calls"] == nil || choice["finish_reason"] != "tool_calls" {
		t.Fatalf("panel calls=%v judge calls=%v response=%s", panelCallsCopy, judgeCallsCopy, response.Body)
	}
}

func TestFusionToolCallHistoryRunsThroughPanelsAndJudge(t *testing.T) {
	strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Model: "judge-model"}}
	candidates := []comboCandidate{{Model: "panel-a"}, {Model: "panel-b"}}
	body := []byte(`{"model":"combo-coding","messages":[{"role":"user","content":"continue"},{"role":"assistant","content":"I’ll inspect the file.","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-1","content":"source text"}],"tools":[{"type":"function","function":{"name":"read_file"}}]}`)
	var panelCalls, judgeCalls []string
	var callsMu sync.Mutex
	_, err := executeWithStrategy(context.Background(), "fusion-tool-history", strategy, candidates, testConfig(), body, "cb", func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
		var payload map[string]any
		if err := json.Unmarshal(request, &payload); err != nil {
			return pluginapi.HostModelExecutionResponse{}, err
		}
		messages, _ := payload["messages"].([]any)
		assistant, _ := messages[1].(map[string]any)
		toolResult, _ := messages[2].(map[string]any)
		if payload["model"] != candidate.Model || len(messages) < 3 || assistant["tool_calls"] == nil || toolResult["role"] != "tool" || toolResult["content"] != "source text" {
			return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("tool history not passed to model=%s: %s", candidate.Model, request)
		}
		if candidate.Model == "judge-model" {
			callsMu.Lock()
			judgeCalls = append(judgeCalls, candidate.Model)
			callsMu.Unlock()
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"done"}}]}`)}, nil
		}
		callsMu.Lock()
		panelCalls = append(panelCalls, candidate.Model)
		callsMu.Unlock()
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"continue"}}]}`)}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callsMu.Lock()
	panelCallsCopy := append([]string(nil), panelCalls...)
	judgeCallsCopy := append([]string(nil), judgeCalls...)
	callsMu.Unlock()
	if len(panelCallsCopy) != 2 || !containsString(panelCallsCopy, "panel-a") || !containsString(panelCallsCopy, "panel-b") || !reflect.DeepEqual(judgeCallsCopy, []string{"judge-model"}) {
		t.Fatalf("panel calls=%v judge calls=%v", panelCallsCopy, judgeCallsCopy)
	}
}

func TestFusionUsesJudgeWhenOnlyOnePanelSucceeds(t *testing.T) {
	candidates := []comboCandidate{{Model: "panel-a"}, {Model: "panel-b"}}
	strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Model: "judge-model"}}
	body, judge, err := prepareFusionPrompt(context.Background(), strategy, candidates, []byte(`{"model":"combo","messages":[{"role":"user","content":"hi"}]}`), "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
		if candidate.Model == "panel-b" {
			return pluginapi.HostModelExecutionResponse{}, StatusError{Code: http.StatusServiceUnavailable}
		}
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"only answer"}}]}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var judgePayload map[string]any
	if err := json.Unmarshal(body, &judgePayload); err != nil {
		t.Fatal(err)
	}
	messages, _ := judgePayload["messages"].([]any)
	judgeCalled := len(messages) > 1 && strings.Contains(fmt.Sprint(messages[len(messages)-1]), "only answer")
	if !judgeCalled || judge.Model != "judge-model" {
		t.Fatalf("single successful panel did not produce a judge request: body=%s judge=%#v", body, judge)
	}
}

func TestFusionUsesJudgeWithSingleConfiguredPanel(t *testing.T) {
	candidates := []comboCandidate{{Model: "panel-a"}}
	strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Model: "judge-model"}}
	calls := 0
	got, err := executeWithStrategy(context.Background(), "fusion-single", strategy, candidates, testConfig(), []byte(`{"model":"combo","messages":[{"role":"user","content":"hi"}]}`), "cb", func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
		calls++
		var payload map[string]any
		if err := json.Unmarshal(request, &payload); err != nil {
			return pluginapi.HostModelExecutionResponse{}, err
		}
		if candidate.Model == "panel-a" {
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"panel answer"}}]}`)}, nil
		}
		messages, _ := payload["messages"].([]any)
		if candidate.Model != "judge-model" || len(messages) < 2 || !strings.Contains(fmt.Sprint(messages[len(messages)-1]), "panel answer") {
			return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("incorrect judge request: model=%s body=%s", candidate.Model, request)
		}
		return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"judged":true}`)}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || string(got.Body) != `{"judged":true}` {
		t.Fatalf("calls=%d response=%s", calls, got.Body)
	}
}

func TestFusionPanelAnswerPreservesNativeToolCallProposals(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "OpenAI chat completions",
			body: `{"choices":[{"message":{"content":"Need inspect source","tool_calls":[{"id":"call-openai","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"src/main.go\"}"}}]}}]}`,
			want: []string{"call-openai", "read_file", "src/main.go", "tool_calls"},
		},
		{
			name: "Responses API",
			body: `{"output":[{"type":"function_call","call_id":"call-responses","name":"read_file","arguments":"{\"path\":\"src/main.go\"}"}]}`,
			want: []string{"call-responses", "read_file", "src/main.go", "tool_calls"},
		},
		{
			name: "Gemini",
			body: `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"read_file","args":{"path":"src/main.go"}}}]}}]}`,
			want: []string{"read_file", "src/main.go", "tool_calls"},
		},
		{
			name: "Anthropic",
			body: `{"content":[{"type":"tool_use","id":"call-anthropic","name":"read_file","input":{"path":"src/main.go"}}]}`,
			want: []string{"call-anthropic", "read_file", "src/main.go", "tool_calls"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fusionPanelAnswer([]byte(tt.body))
			for _, part := range tt.want {
				if !strings.Contains(got, part) {
					t.Fatalf("panel proposal %q missing %q", got, part)
				}
			}
		})
	}
}

func TestFusionPreservesResponsesAPIAndGeminiToolHistory(t *testing.T) {
	body := []byte(`{"model":"combo","input":[{"type":"message","role":"assistant","content":"Earlier relevant context."},{"type":"message","role":"user","content":"question"},{"type":"message","role":"assistant","content":"I'll compare the results using lookup now."},{"type":"message","role":"assistant","content":"Looking up the answer."},{"type":"function_call","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call-1","output":{"value":42}}],"tools":[{}],"parallel_tool_calls":true,"stream":true}`)
	got, err := fusionPanelBody(body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 6 || payload["tools"] == nil || payload["parallel_tool_calls"] != true || payload["stream"] != false {
		t.Fatalf("Fusion did not preserve the Responses tool request: %s", got)
	}
	inputJSON, _ := json.Marshal(input)
	inputText := string(inputJSON)
	if !strings.Contains(inputText, "Earlier relevant context.") || !strings.Contains(inputText, "question") || !strings.Contains(inputText, `{"value":42}`) || !strings.Contains(inputText, "I'll compare") || !strings.Contains(inputText, "Looking up the answer.") || !strings.Contains(inputText, "function_call") || !strings.Contains(inputText, "lookup") {
		t.Fatalf("Responses tool history or preamble was lost: %#v", input)
	}
	gemini := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}},{"functionResponse":{"name":"lookup","response":{"value":"ok"}}}]}]}`)
	got, err = fusionPanelBody(gemini)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"functionCall"`) || !strings.Contains(string(got), `"functionResponse"`) || !strings.Contains(string(got), `"name":"lookup"`) || !strings.Contains(string(got), `"stream":false`) {
		t.Fatalf("Gemini tool history was not preserved for Fusion: %s", got)
	}
	thoughtResponse := []byte(`{"candidates":[{"content":{"parts":[{"text":"private chain of thought","thought":true},{"text":"user-facing answer"}]}}]}`)
	if got := responseText(thoughtResponse); got != "user-facing answer" {
		t.Fatalf("Gemini response text included internal thoughts: %q", got)
	}
}

func TestValidateStrategies(t *testing.T) {
	cfg := testConfig()
	cfg.Strategies = map[string]comboStrategy{"combo-coding": {Type: "smart", Selector: comboCandidate{Model: "selector"}, ConfidenceThreshold: 0.75}}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("valid smart strategy rejected: %v", err)
	}
	cfg.Strategies["combo-coding"] = comboStrategy{Type: "unknown"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("unsupported strategy accepted")
	}
	cfg.Strategies["combo-coding"] = comboStrategy{Type: "smart"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("smart strategy without selector accepted")
	}
	cfg.Strategies["combo-coding"] = comboStrategy{Type: "fallback", VisionEnabled: true}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "no vision_candidates") {
		t.Fatalf("vision routing without candidates error = %v", err)
	}
	cfg.Strategies["combo-coding"] = comboStrategy{Type: "fallback", VisionEnabled: true, VisionCandidates: []comboCandidate{{Model: " vision-model "}}}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "vision candidate") {
		t.Fatalf("invalid vision candidate error = %v", err)
	}
	cfg.Strategies["combo-coding"] = comboStrategy{Type: "fusion", VisionEnabled: true, VisionCandidates: []comboCandidate{{Model: "vision-model"}}, VisionJudge: comboCandidate{Provider: "only-provider"}}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "vision judge") {
		t.Fatalf("invalid vision judge error = %v", err)
	}
	cfg.Strategies["combo-coding"] = comboStrategy{Type: "fallback", VisionCandidates: []comboCandidate{{Model: "vision-model"}}}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("disabled vision routing with configured pool rejected: %v", err)
	}
}

func TestRequestHasImagePayloadDetectsCommonFormatsAndIgnoresText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "OpenAI image_url object", body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}]}`, want: true},
		{name: "OpenAI Responses input_image", body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`, want: true},
		{name: "Anthropic image source", body: `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}`, want: true},
		{name: "Gemini inline_data", body: `{"contents":[{"role":"user","parts":[{"inline_data":{"mime_type":"image/jpeg","data":"/9j/4AAQSkZJRg=="}}]}]}`, want: true},
		{name: "Gemini fileData", body: `{"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"image/webp","fileUri":"https://example.test/file/1"}}]}]}`, want: true},
		{name: "text mentioning image", body: `{"messages":[{"role":"user","content":"Please describe the image at https://example.test/image.png"}]}`, want: false},
		{name: "non-image inline data", body: `{"contents":[{"parts":[{"inline_data":{"mime_type":"application/json","data":"{}"}}]}]}`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestHasImagePayload([]byte(tt.body)); got != tt.want {
				t.Fatalf("requestHasImagePayload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImageTriggeredVisionRoutingAcrossStrategies(t *testing.T) {
	regular := []comboCandidate{{Model: "text-primary"}, {Model: "text-fallback"}}
	vision := []comboCandidate{{Model: "vision-primary"}, {Model: "vision-fallback"}}
	cfg := testConfig()
	imageBody := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
	textBody := []byte(`{"messages":[{"role":"user","content":"Inspect the source"}]}`)

	t.Run("fallback selects vision pool and falls through within it", func(t *testing.T) {
		strategy := comboStrategy{Type: "fallback", VisionEnabled: true, VisionCandidates: vision}
		var calls []string
		got, err := executeWithStrategy(context.Background(), "combo-coding", strategy, regular, cfg, imageBody, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			calls = append(calls, candidate.Model)
			if candidate.Model == "vision-primary" {
				return pluginapi.HostModelExecutionResponse{}, StatusError{Code: http.StatusServiceUnavailable}
			}
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
		}, nil)
		if err != nil || got.StatusCode != http.StatusOK {
			t.Fatalf("response=%#v err=%v", got, err)
		}
		if !reflect.DeepEqual(calls, []string{"vision-primary", "vision-fallback"}) {
			t.Fatalf("calls=%v, want ordered vision pool", calls)
		}
	})

	t.Run("text request remains on regular pool", func(t *testing.T) {
		strategy := comboStrategy{Type: "fallback", VisionEnabled: true, VisionCandidates: vision}
		var calls []string
		_, err := executeWithStrategy(context.Background(), "combo-coding", strategy, regular, cfg, textBody, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			calls = append(calls, candidate.Model)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
		}, nil)
		if !reflect.DeepEqual(calls, []string{"text-primary"}) {
			t.Fatalf("calls=%v err=%v, want only regular primary", calls, err)
		}
	})

	t.Run("disabled vision routing leaves image request on regular pool", func(t *testing.T) {
		strategy := comboStrategy{Type: "fallback", VisionEnabled: false, VisionCandidates: vision}
		var calls []string
		_, err := executeWithStrategy(context.Background(), "combo-coding", strategy, regular, cfg, imageBody, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			calls = append(calls, candidate.Model)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
		}, nil)
		if err != nil || !reflect.DeepEqual(calls, []string{"text-primary"}) {
			t.Fatalf("calls=%v err=%v, want regular primary while vision routing is disabled", calls, err)
		}
	})

	t.Run("round robin rotates within vision pool", func(t *testing.T) {
		resetComboRotations()
		strategy := comboStrategy{Type: "round-robin", StickyLimit: 1, VisionEnabled: true, VisionCandidates: vision}
		var calls [2][]string
		for i := range calls {
			_, err := executeWithStrategy(context.Background(), "combo-coding", strategy, regular, cfg, imageBody, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
				calls[i] = append(calls[i], candidate.Model)
				return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(calls, [2][]string{{"vision-primary"}, {"vision-fallback"}}) {
			t.Fatalf("round-robin calls=%v", calls)
		}
	})

	t.Run("smart chooses only among vision candidates", func(t *testing.T) {
		strategy := comboStrategy{Type: "smart", Selector: comboCandidate{Model: "selector"}, ConfidenceThreshold: 0.8, VisionEnabled: true, VisionCandidates: vision}
		var calls []string
		var selectorRequest []byte
		_, err := executeWithStrategy(context.Background(), "combo-coding", strategy, regular, cfg, imageBody, "cb", func(_ context.Context, candidate comboCandidate, _ []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			calls = append(calls, candidate.Model)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}, nil
		}, func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			if candidate.Model != "selector" {
				t.Fatalf("selector candidate=%#v", candidate)
			}
			selectorRequest = append([]byte(nil), request...)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"{\"choice\":\"/vision-fallback\",\"confidence\":0.95}"}}]}`)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(selectorRequest), "/vision-primary") || !strings.Contains(string(selectorRequest), "/vision-fallback") || strings.Contains(string(selectorRequest), "text-primary") {
			t.Fatalf("selector did not receive vision pool only: %s", selectorRequest)
		}
		if !reflect.DeepEqual(calls, []string{"vision-fallback"}) {
			t.Fatalf("selected candidate calls=%v", calls)
		}
	})

	t.Run("fusion uses vision panels and vision judge with original image", func(t *testing.T) {
		strategy := comboStrategy{Type: "fusion", Judge: comboCandidate{Model: "text-judge"}, VisionEnabled: true, VisionCandidates: vision, VisionJudge: comboCandidate{Model: "vision-judge"}}
		var panels []string
		var judgeBody []byte
		var mu sync.Mutex
		_, err := executeWithStrategy(context.Background(), "combo-coding", strategy, regular, cfg, imageBody, "cb", func(_ context.Context, candidate comboCandidate, request []byte, _ string) (pluginapi.HostModelExecutionResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if candidate.Model == "vision-judge" {
				judgeBody = append([]byte(nil), request...)
				return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"choices":[{"message":{"content":"visual synthesis"}}]}`)}, nil
			}
			panels = append(panels, candidate.Model)
			proposal := fmt.Sprintf(`{"choices":[{"message":{"content":"proposal from %s"}}]}`, candidate.Model)
			return pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(proposal)}, nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(map[string]bool{panels[0]: true, panels[1]: true}, map[string]bool{"vision-primary": true, "vision-fallback": true}) {
			t.Fatalf("fusion panels=%v", panels)
		}
		if !requestHasImagePayload(judgeBody) || !strings.Contains(string(judgeBody), "proposal from vision-primary") || !strings.Contains(string(judgeBody), "proposal from vision-fallback") || strings.Contains(string(judgeBody), "text-judge") {
			t.Fatalf("vision judge request missing original image/panel proposals or used wrong judge: %s", judgeBody)
		}
	})
}

func TestImageTriggeredVisionRoutingInStreamingExecutor(t *testing.T) {
	cfg := testConfig()
	cfg.Strategies = map[string]comboStrategy{"combo-coding": {Type: "fallback", VisionEnabled: true, VisionCandidates: []comboCandidate{{Model: "vision-stream"}}}}
	currentConfig.Store(cfg)
	body := []byte(`{"model":"combo-coding","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}],"stream":true}`)
	request, err := json.Marshal(rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{Model: "combo-coding", SourceFormat: "openai", Stream: true, OriginalRequest: body}, StreamID: "vision-stream", HostCallbackID: "callback"})
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	var callMu sync.Mutex
	var models []string
	response, err := executeStreamWithHost(request, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostModelExecuteStream:
			hostReq := payload.(hostModelExecutionRequest)
			callMu.Lock()
			models = append(models, hostReq.Model)
			callMu.Unlock()
			if hostReq.Model != "vision-stream" || !requestHasImagePayload(hostReq.Body) {
				return nil, fmt.Errorf("stream was not routed with image to vision model: model=%q body=%s", hostReq.Model, hostReq.Body)
			}
			return json.Marshal(pluginapi.HostModelStreamResponse{StatusCode: http.StatusOK, StreamID: "upstream-vision"})
		case pluginabi.MethodHostModelStreamRead:
			return json.Marshal(pluginapi.HostModelStreamReadResponse{Payload: []byte("data: ok\n\n"), Done: true})
		case pluginabi.MethodHostStreamEmit:
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostModelStreamClose:
			return json.Marshal(map[string]any{})
		case pluginabi.MethodHostStreamClose:
			close(closed)
			return json.Marshal(map[string]any{})
		default:
			return nil, fmt.Errorf("unexpected host method %q", method)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var responseEnvelope envelope
	if err := json.Unmarshal(response, &responseEnvelope); err != nil || !responseEnvelope.OK {
		t.Fatalf("stream start response=%s err=%v", response, err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for image stream completion")
	}
	callMu.Lock()
	defer callMu.Unlock()
	if !reflect.DeepEqual(models, []string{"vision-stream"}) {
		t.Fatalf("stream models=%v, want vision model only", models)
	}
}

func TestStatusErrorExtractionSupportsWrappedStatusAndRejectsInvalidEnvelope(t *testing.T) {
	wrapped := fmt.Errorf("host callback: %w", StatusError{Code: http.StatusTooManyRequests})
	if got, ok := AsStatusError(wrapped); !ok || got.Code != http.StatusTooManyRequests {
		t.Fatalf("wrapped status=%#v ok=%v", got, ok)
	}
	for _, message := range []string{"", "not json", `{"http_status":0}`, `{"http_status":"429"}`} {
		if got := decodeStatusError(errors.New(message)); got == nil || got.Error() != message {
			t.Fatalf("decodeStatusError(%q) = %v", message, got)
		}
	}
}
