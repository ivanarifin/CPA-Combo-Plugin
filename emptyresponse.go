package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// errEmptyResponse marks a successful-looking reply with nothing in it. Gemini, for one,
// can abort with MALFORMED_FUNCTION_CALL and still return 200 with a blank final frame.
var errEmptyResponse = StatusError{Code: http.StatusBadGateway, Message: "model returned an empty response"}

// streamOutputPeek holds a stream's opening frames until real output appears, so a blank
// reply can fall through to the next attempt before anything reaches the client. Reasoning
// counts as output, so thinking still shows early, and anything it cannot recognize passes
// through untouched, so an unfamiliar format can never be blocked.
type streamOutputPeek struct {
	emit    func([]byte) error
	held    [][]byte
	raw     bytes.Buffer
	flushed bool
}

func (p *streamOutputPeek) write(chunk []byte) error {
	if len(chunk) == 0 {
		return p.emit(chunk)
	}
	if p.flushed {
		return p.emit(chunk)
	}
	p.held = append(p.held, append([]byte(nil), chunk...))
	p.raw.Write(chunk)
	if !streamHasOutput(p.raw.Bytes(), false) {
		return nil
	}
	return p.flush()
}

func (p *streamOutputPeek) flush() error {
	p.flushed = true
	held := p.held
	p.held = nil
	p.raw = bytes.Buffer{}
	for _, chunk := range held {
		if err := p.emit(chunk); err != nil {
			return err
		}
	}
	return nil
}

// finish runs when the stream ended without an error. A stream that only ever held
// recognized metadata is blank; one that sent nothing at all is left to the host.
func (p *streamOutputPeek) finish() error {
	if p.flushed || len(p.held) == 0 {
		return nil
	}
	if streamHasOutput(p.raw.Bytes(), true) {
		return p.flush()
	}
	return errEmptyResponse
}

// streamHasOutput inspects buffered frames. Until atEnd, an unterminated tail may be half a
// frame, so it is left for the next chunk rather than treated as unknown data.
func streamHasOutput(raw []byte, atEnd bool) bool {
	text := string(raw)
	sawData := false
	for {
		line, rest, complete := strings.Cut(text, "\n")
		if !complete {
			break
		}
		text = rest
		data, found := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !found {
			continue
		}
		sawData = true
		if streamEventHasOutput(strings.TrimSpace(data)) {
			return true
		}
	}
	tail := strings.TrimSpace(text)
	if data, found := strings.CutPrefix(tail, "data:"); found {
		return sseTailHasOutput(strings.TrimSpace(data), atEnd)
	}
	if sawData || tail == "" || strings.HasPrefix(tail, "event:") || strings.HasPrefix(strings.TrimSpace(string(raw)), "event:") {
		return false
	}
	return bareStreamHasOutput(raw, atEnd)
}

// sseTailHasOutput judges a final frame with no newline yet. Complete JSON and plain text
// can be judged now; only what looks like half a JSON value waits for the next chunk.
func sseTailHasOutput(data string, atEnd bool) bool {
	if json.Valid([]byte(data)) || !(strings.HasPrefix(data, "{") || strings.HasPrefix(data, "[")) {
		return streamEventHasOutput(data)
	}
	return atEnd
}

// bareStreamHasOutput handles CPA's OpenAI and Gemini chunks, which arrive as plain JSON
// values with the SSE framing added later.
func bareStreamHasOutput(raw []byte, atEnd bool) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var value json.RawMessage
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			rest := strings.TrimSpace(string(raw[decoder.InputOffset():]))
			if strings.HasPrefix(rest, "[DONE]") {
				return false
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return atEnd
			}
			return true
		}
		var events []json.RawMessage
		if json.Unmarshal(value, &events) != nil {
			events = []json.RawMessage{value}
		}
		for _, event := range events {
			if streamEventHasOutput(string(event)) {
				return true
			}
		}
	}
}

func streamEventHasOutput(data string) bool {
	if data == "" || data == "[DONE]" {
		return false
	}
	var event map[string]any
	if json.Unmarshal([]byte(data), &event) != nil || event == nil {
		return true
	}
	if event["error"] != nil {
		return true
	}
	if choices, ok := event["choices"].([]any); ok {
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			for _, key := range []string{"delta", "message"} {
				if message, ok := choice[key].(map[string]any); ok && chatMessageHasOutput(message) {
					return true
				}
			}
		}
		return false
	}
	if candidates, ok := event["candidates"].([]any); ok {
		for _, raw := range candidates {
			candidate, _ := raw.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if text, ok := part["text"].(string); ok && text == "" && len(part) == 1 {
					continue
				}
				if len(part) > 0 {
					return true
				}
			}
		}
		return false
	}
	kind, hasKind := event["type"].(string)
	if !hasKind {
		return true
	}
	return typedEventHasOutput(kind, event)
}

func chatMessageHasOutput(message map[string]any) bool {
	for _, key := range []string{"content", "reasoning_content", "reasoning"} {
		switch value := message[key].(type) {
		case string:
			if value != "" {
				return true
			}
		case []any:
			if len(value) > 0 {
				return true
			}
		}
	}
	for _, key := range []string{"tool_calls", "reasoning_details"} {
		if list, ok := message[key].([]any); ok && len(list) > 0 {
			return true
		}
	}
	return message["function_call"] != nil
}

// typedEventHasOutput covers Claude and Responses events. Only known metadata counts as
// blank: an event type it has never seen is treated as output.
func typedEventHasOutput(kind string, event map[string]any) bool {
	switch kind {
	case "message_start", "message_delta", "message_stop", "ping", "content_block_stop",
		"response.created", "response.in_progress", "response.queued",
		"response.content_part.added", "response.content_part.done",
		"response.output_text.done", "response.output_item.done":
		return false
	case "content_block_start":
		block, _ := event["content_block"].(map[string]any)
		text, isText := block["text"].(string)
		return !(block["type"] == "text" && isText && text == "") && block["type"] != "thinking"
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		for _, key := range []string{"text", "thinking", "partial_json"} {
			if value, _ := delta[key].(string); value != "" {
				return true
			}
		}
		return false
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		return item["type"] != "message" && item["type"] != "reasoning"
	case "response.completed", "response.incomplete", "response.done":
		response, _ := event["response"].(map[string]any)
		output, _ := response["output"].([]any)
		return len(output) > 0
	}
	if strings.HasSuffix(kind, ".delta") {
		delta, _ := event["delta"].(string)
		return delta != ""
	}
	return true
}

// responseBodyIsEmpty applies the same rule to a finished response. Only a recognized reply
// shape can be blank, so unfamiliar bodies are returned to the client as they are.
func responseBodyIsEmpty(raw []byte) bool {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return false
	}
	recognized := false
	for _, key := range []string{"choices", "candidates", "output"} {
		if _, ok := payload[key].([]any); ok {
			recognized = true
		}
	}
	if _, ok := payload["content"].([]any); ok && payload["role"] == "assistant" {
		recognized = true
	}
	// CPA omits "output" altogether from a blank Responses reply.
	if payload["object"] == "response" {
		recognized = true
	}
	if !recognized {
		return false
	}
	text, calls := fusionPanelParts(raw)
	return text == "" && calls == nil
}
