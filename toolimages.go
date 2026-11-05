package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	toolImageMovedNote     = "[Image moved to the next user message.]"
	toolImagesMessageLabel = "Images returned by the tool calls above:"
)

// moveToolResultImages rewrites chat-format requests so images returned by tools travel in
// a user message right after the tool results. Some CPA translators, including Antigravity's,
// stringify tool content, which turns a screenshot's base64 into text the model counts token
// by token: one 800x800 PNG overflowed Gemini's 1M limit this way, against about 1,100 tokens
// as a user-message image. It returns the body unchanged when there is nothing to move.
func moveToolResultImages(body []byte) ([]byte, int, int) {
	if !bytes.Contains(body, []byte("image_url")) {
		return body, 0, 0
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body, 0, 0
	}
	messages, ok := payload["messages"].([]any)
	if !ok {
		return body, 0, 0
	}
	out := make([]any, 0, len(messages)+1)
	var pending []any
	moved, size := 0, 0
	flush := func() {
		if len(pending) == 0 {
			return
		}
		content := append([]any{map[string]any{"type": "text", "text": toolImagesMessageLabel}}, pending...)
		out = append(out, map[string]any{"role": "user", "content": content})
		pending = nil
	}
	for _, raw := range messages {
		message, isObject := raw.(map[string]any)
		if !isObject || message["role"] != "tool" {
			flush()
			out = append(out, raw)
			continue
		}
		parts, isList := message["content"].([]any)
		if !isList {
			out = append(out, raw)
			continue
		}
		var texts []string
		found := 0
		kept := make([]any, 0, len(parts))
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if part["type"] == "image_url" {
				pending = append(pending, part)
				found++
				size += toolImageSize(part)
				continue
			}
			kept = append(kept, rawPart)
			if text, ok := part["text"].(string); ok && part["type"] == "text" {
				texts = append(texts, text)
			}
		}
		if found == 0 {
			out = append(out, raw)
			continue
		}
		moved += found
		copied := make(map[string]any, len(message))
		for key, value := range message {
			copied[key] = value
		}
		if len(texts) == len(kept) {
			copied["content"] = strings.Join(append(texts, toolImageMovedNote), "\n")
		} else {
			copied["content"] = append(kept, map[string]any{"type": "text", "text": toolImageMovedNote})
		}
		out = append(out, copied)
	}
	flush()
	if moved == 0 {
		return body, 0, 0
	}
	payload["messages"] = out
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, 0, 0
	}
	return encoded, moved, size
}

func toolImageSize(part map[string]any) int {
	image, _ := part["image_url"].(map[string]any)
	url, _ := image["url"].(string)
	return len(url)
}

// candidateRequestBody is the last step before any model call, so every strategy gets it.
func candidateRequestBody(candidate comboCandidate, body []byte) []byte {
	request, moved, size := moveToolResultImages(body)
	if moved > 0 {
		logComboEvent("tool_images_moved", "", "", fmt.Sprintf(" model=%q images=%d bytes=%d", candidate.Model, moved, size))
	}
	return request
}
