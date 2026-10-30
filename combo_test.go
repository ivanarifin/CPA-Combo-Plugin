package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
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
	for _, expected := range []string{"New combo alias", "Add candidate model", "Save combo settings", "Choose model ID", "Enter a custom model ID…", "Current custom model:", "refreshModelsBtn", "clientKeyForm", "clientAPIKey", "CPA client API key", "Forget key", "sessionStorage.setItem(CLIENT_KEY_STORAGE,value)", "base+'/v1/models'", "'Authorization':'Bearer '+clientAPIKey", "payload?.data", "owned_by", "id=\"saveFooter\"", "updatePendingChanges()", "retry_statuses", "/v0/management/plugins/", "/config", pluginID} {
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
		{name: "disabled without chains", mutate: func(c *comboConfig) { c.Enabled = false; c.ModelChains = nil }},
		{name: "enabled without chains", mutate: func(c *comboConfig) { c.ModelChains = nil }, wantErr: true},
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
