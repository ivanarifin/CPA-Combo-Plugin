package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	comboTurnTTL   = 2 * time.Hour
	comboTurnLimit = 4096
)

type comboTurnEntry struct {
	candidate comboCandidate
	expires   time.Time
}

// comboTurns remembers which model is serving a user message, so the steps of one agent
// task stay on one model. Keys are hashes, so no user text is held in memory.
var comboTurns = struct {
	sync.Mutex
	entries map[string]comboTurnEntry
}{entries: make(map[string]comboTurnEntry)}

func resetComboTurns() {
	comboTurns.Lock()
	comboTurns.entries = make(map[string]comboTurnEntry)
	comboTurns.Unlock()
}

// comboTurnPin ties one request to its entry. The zero value does nothing, which is
// what strategies without stickiness use.
type comboTurnPin struct {
	key      string
	followup bool
}

func newComboTurnPin(pool string, body []byte) comboTurnPin {
	first, latest, ok := userTextBounds(body)
	if !ok {
		return comboTurnPin{}
	}
	sum := sha256.Sum256([]byte(pool + "\x00" + first + "\x00" + latest))
	return comboTurnPin{key: hex.EncodeToString(sum[:]), followup: requestIsToolFollowup(body)}
}

// pinned returns the model already serving this user message. Only tool follow-ups
// reuse a pin: a new user message is a new decision even with identical text.
func (p comboTurnPin) pinned(candidates []comboCandidate) (comboCandidate, bool) {
	if p.key == "" || !p.followup {
		return comboCandidate{}, false
	}
	comboTurns.Lock()
	entry, ok := comboTurns.entries[p.key]
	if ok && time.Now().After(entry.expires) {
		delete(comboTurns.entries, p.key)
		ok = false
	}
	comboTurns.Unlock()
	if !ok {
		return comboCandidate{}, false
	}
	for _, candidate := range candidates {
		if candidate == entry.candidate {
			return candidate, true
		}
	}
	return comboCandidate{}, false
}

// remember records the model that actually answered, so a task that fell back stays on
// the working model instead of retrying the failed one on every step.
func (p comboTurnPin) remember(candidate comboCandidate) {
	if p.key == "" {
		return
	}
	now := time.Now()
	comboTurns.Lock()
	defer comboTurns.Unlock()
	if _, exists := comboTurns.entries[p.key]; !exists && len(comboTurns.entries) >= comboTurnLimit {
		evictComboTurnsLocked(now)
	}
	comboTurns.entries[p.key] = comboTurnEntry{candidate: candidate, expires: now.Add(comboTurnTTL)}
}

func evictComboTurnsLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for key, entry := range comboTurns.entries {
		if now.After(entry.expires) {
			delete(comboTurns.entries, key)
			continue
		}
		if oldestKey == "" || entry.expires.Before(oldest) {
			oldestKey, oldest = key, entry.expires
		}
	}
	if len(comboTurns.entries) >= comboTurnLimit {
		delete(comboTurns.entries, oldestKey)
	}
}

func (p comboTurnPin) observe(call hostModelCall) hostModelCall {
	if p.key == "" {
		return call
	}
	return func(ctx context.Context, candidate comboCandidate, body []byte, callbackID string) (pluginapi.HostModelExecutionResponse, error) {
		response, err := call(ctx, candidate, body, callbackID)
		if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			p.remember(candidate)
		}
		return response, err
	}
}

// requestIsToolFollowup reports whether the conversation ends in a tool result, meaning
// this request continues an agent task rather than starting one.
func requestIsToolFollowup(body []byte) bool {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	lastItem := func(key string) (map[string]any, bool) {
		items, ok := payload[key].([]any)
		if !ok || len(items) == 0 {
			return nil, false
		}
		item, _ := items[len(items)-1].(map[string]any)
		return item, true
	}
	if last, ok := lastItem("messages"); ok {
		if last["role"] == "tool" || last["role"] == "function" {
			return true
		}
		return blocksHaveToolResult(last["content"])
	}
	if last, ok := lastItem("input"); ok {
		kind, _ := last["type"].(string)
		return strings.HasSuffix(kind, "_output")
	}
	if last, ok := lastItem("contents"); ok {
		return last["role"] == "function" || blocksHaveToolResult(last["parts"])
	}
	return false
}

func blocksHaveToolResult(value any) bool {
	blocks, ok := value.([]any)
	if !ok {
		return false
	}
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block["type"] == "tool_result" || block["functionResponse"] != nil || block["function_response"] != nil {
			return true
		}
	}
	return false
}

// userMessageTexts returns the text of real user messages. Claude and Gemini carry tool
// results as user-role items, which must not count as something the user asked.
func userMessageTexts(payload map[string]any) []string {
	for _, field := range []struct{ key, content string }{{"contents", "parts"}, {"messages", "content"}, {"input", "content"}} {
		items, ok := payload[field.key].([]any)
		if !ok {
			continue
		}
		var texts []string
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			if item["role"] == "user" && !blocksHaveToolResult(item[field.content]) {
				texts = append(texts, valueText(item[field.content]))
			}
		}
		if len(texts) > 0 {
			return texts
		}
	}
	return nil
}

// userTextBounds returns the conversation's first and latest user text.
func userTextBounds(body []byte) (string, string, bool) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", "", false
	}
	if input, ok := payload["input"].(string); ok && input != "" {
		return input, input, true
	}
	if texts := userMessageTexts(payload); len(texts) > 0 {
		first, latest := texts[0], texts[len(texts)-1]
		return first, latest, first != "" || latest != ""
	}
	prompt := valueText(payload["prompt"])
	return prompt, prompt, prompt != ""
}

func latestUserText(raw []byte) string {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return string(raw)
	}
	_, latest, _ := userTextBounds(raw)
	return latest
}

func rotateToCandidate(candidates []comboCandidate, selected comboCandidate) []comboCandidate {
	for index, candidate := range candidates {
		if candidate == selected {
			return append(append([]comboCandidate(nil), candidates[index:]...), candidates[:index]...)
		}
	}
	return append([]comboCandidate(nil), candidates...)
}

// roundRobinOrder keeps an agent task on its model and only advances the rotation when a
// new user message arrives, unless rotate_per_request restores per-call rotation.
func roundRobinOrder(pool string, strategy comboStrategy, candidates []comboCandidate, pin comboTurnPin) []comboCandidate {
	if selected, ok := pin.pinned(candidates); ok {
		return rotateToCandidate(candidates, selected)
	}
	return rotateCandidates(pool, candidates, strategy.StickyLimit)
}

func roundRobinPin(pool string, strategy comboStrategy, body []byte) comboTurnPin {
	if strategy.RotatePerRequest {
		return comboTurnPin{}
	}
	return newComboTurnPin(pool, body)
}
