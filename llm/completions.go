package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// StreamOptions configures a single request.
type StreamOptions struct {
	Temperature    *float64
	MaxTokens      int
	APIKey         string
	CacheRetention CacheRetention
	SessionID      string
	Headers        map[string]string
	Timeout        time.Duration
	Reasoning      ThinkingLevel
	ToolChoice     any

	// OnPayload may inspect or replace the request body before sending. Return
	// nil to keep the payload unchanged.
	OnPayload func(payload map[string]any) map[string]any
	// OnResponse is called with the HTTP status and headers before the body is read.
	OnResponse func(status int, headers map[string]string)

	reasoningEffort ThinkingLevel
}

// StreamSimple issues a streaming chat-completions request and returns a Stream.
// It never returns an error directly; failures are delivered as an ErrorEvent
// and a final assistant message with StopReason error/aborted.
func StreamSimple(ctx context.Context, model *Model, reqCtx *Context, opts *StreamOptions) *Stream {
	if opts == nil {
		opts = &StreamOptions{}
	}
	o := *opts
	if o.APIKey == "" {
		o.APIKey = envAPIKey(model.provider())
	}
	if o.Reasoning != "" {
		clamped := ClampThinkingLevel(model, o.Reasoning)
		if clamped != ThinkingOff {
			o.reasoningEffort = clamped
		}
	}
	return streamOpenAICompletions(ctx, model, reqCtx, &o)
}

// CompleteSimple runs a request and returns the final assistant message.
func CompleteSimple(ctx context.Context, model *Model, reqCtx *Context, opts *StreamOptions) *AssistantMessage {
	return StreamSimple(ctx, model, reqCtx, opts).Result()
}

func resolveCacheRetention(r CacheRetention) CacheRetention {
	if r != "" {
		return r
	}
	if os.Getenv("PI_CACHE_RETENTION") == "long" {
		return CacheLong
	}
	return CacheShort
}

func clampCacheKey(key string) string {
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) <= 64 {
		return key
	}
	return string(r[:64])
}

func newAssistant(model *Model) *AssistantMessage {
	return &AssistantMessage{
		API:        apiOpenAICompletions,
		Provider:   model.provider(),
		Model:      model.ID,
		StopReason: StopEnd,
		Timestamp:  time.Now(),
	}
}

func streamOpenAICompletions(ctx context.Context, model *Model, reqCtx *Context, opts *StreamOptions) *Stream {
	stream := newStream()
	output := newAssistant(model)

	go func() {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			err = runCompletion(ctx, model, reqCtx, opts, stream, output)
		}()
		if err != nil {
			for _, c := range output.Content {
				if tc, ok := c.(*ToolCall); ok {
					tc.partialArgs = ""
					tc.hasIndex = false
				}
			}
			if ctx.Err() != nil {
				output.StopReason = StopAborted
			} else {
				output.StopReason = StopError
			}
			output.ErrorMessage = err.Error()
			stream.push(ErrorEvent{baseEvent{output}, output.StopReason, output})
		}
		stream.close(output)
	}()

	return stream
}

func runCompletion(ctx context.Context, model *Model, reqCtx *Context, opts *StreamOptions, stream *Stream, output *AssistantMessage) error {
	if opts.APIKey == "" {
		return fmt.Errorf("no API key for provider: %s", model.provider())
	}

	compat := getCompat(model)
	retention := resolveCacheRetention(opts.CacheRetention)
	params := buildParams(model, reqCtx, opts, compat, retention)

	if opts.OnPayload != nil {
		if replaced := opts.OnPayload(params); replaced != nil {
			params = replaced
		}
	}

	body, err := json.Marshal(params)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, model.baseURL()+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	if opts.SessionID != "" && compat.sendSessionAffinityHeaders && retention != CacheNone {
		req.Header.Set("session_id", opts.SessionID)
		req.Header.Set("x-client-request-id", opts.SessionID)
		req.Header.Set("x-session-affinity", opts.SessionID)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{}
	if opts.Timeout > 0 {
		client.Timeout = opts.Timeout
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if opts.OnResponse != nil {
		headers := make(map[string]string, len(resp.Header))
		for k := range resp.Header {
			headers[k] = resp.Header.Get(k)
		}
		opts.OnResponse(resp.StatusCode, headers)
	}

	if resp.StatusCode/100 != 2 {
		return httpError(resp)
	}

	stream.push(StartEvent{baseEvent{output}})
	return consumeSSE(ctx, resp.Body, model, stream, output)
}

func httpError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	msg := fmt.Sprintf("request failed: %s", resp.Status)
	var parsed struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &parsed) == nil {
		if parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		if parsed.Error.Metadata.Raw != "" {
			msg += "\n" + parsed.Error.Metadata.Raw
		}
	} else if len(data) > 0 {
		msg += ": " + string(data)
	}
	return fmt.Errorf("%s", msg)
}

func buildParams(model *Model, reqCtx *Context, opts *StreamOptions, compat resolvedCompat, retention CacheRetention) map[string]any {
	messages := convertMessages(model, reqCtx, compat)

	params := map[string]any{
		"model":    model.ID,
		"messages": messages,
		"stream":   true,
	}

	baseURL := model.baseURL()
	if (strings.Contains(baseURL, "api.openai.com") && retention != CacheNone) ||
		(retention == CacheLong && compat.supportsLongCacheRetention) {
		if key := clampCacheKey(opts.SessionID); key != "" {
			params["prompt_cache_key"] = key
		}
	}
	if retention == CacheLong && compat.supportsLongCacheRetention {
		params["prompt_cache_retention"] = "24h"
	}
	if compat.supportsUsageInStreaming {
		params["stream_options"] = map[string]any{"include_usage": true}
	}
	if compat.supportsStore {
		params["store"] = false
	}
	if opts.MaxTokens > 0 {
		params[compat.maxTokensField] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		params["temperature"] = *opts.Temperature
	}

	var tools []map[string]any
	if len(reqCtx.Tools) > 0 {
		tools = convertTools(reqCtx.Tools, compat)
		params["tools"] = tools
	} else if hasToolHistory(reqCtx.Messages) {
		tools = []map[string]any{}
		params["tools"] = tools
	}

	if cc := compatCacheControl(compat, retention); cc != nil {
		applyAnthropicCacheControl(messages, tools, cc)
	}

	if opts.ToolChoice != nil {
		params["tool_choice"] = opts.ToolChoice
	}

	applyReasoning(model, opts, compat, params)

	if strings.Contains(baseURL, "openrouter.ai") && compat.openRouterRouting != nil {
		params["provider"] = compat.openRouterRouting
	}

	return params
}

func applyReasoning(model *Model, opts *StreamOptions, compat resolvedCompat, params map[string]any) {
	if !model.Reasoning {
		return
	}
	effort := opts.reasoningEffort
	on := effort != ""
	mapped := func(level ThinkingLevel) string {
		if v, ok := model.ThinkingLevelMap[level]; ok {
			return v
		}
		return string(level)
	}

	switch compat.thinkingFormat {
	case ThinkingFormatZAI, ThinkingFormatQwen:
		params["enable_thinking"] = on
	case ThinkingFormatDeepSeek:
		if on {
			params["thinking"] = map[string]any{"type": "enabled"}
			params["reasoning_effort"] = mapped(effort)
		} else {
			params["thinking"] = map[string]any{"type": "disabled"}
		}
	case ThinkingFormatOpenRouter:
		if on {
			params["reasoning"] = map[string]any{"effort": mapped(effort)}
		} else if !model.NullLevels[ThinkingOff] {
			offVal := "none"
			if v, ok := model.ThinkingLevelMap[ThinkingOff]; ok {
				offVal = v
			}
			params["reasoning"] = map[string]any{"effort": offVal}
		}
	case ThinkingFormatTogether:
		params["reasoning"] = map[string]any{"enabled": on}
		if on && compat.supportsReasoningEffort {
			params["reasoning_effort"] = mapped(effort)
		}
	default:
		if !compat.supportsReasoningEffort {
			return
		}
		if on {
			params["reasoning_effort"] = mapped(effort)
		} else if v, ok := model.ThinkingLevelMap[ThinkingOff]; ok && v != "" {
			params["reasoning_effort"] = v
		}
	}
}
