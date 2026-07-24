package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	// Headers are merged over provider defaults; an empty value suppresses a
	// default header with the same name.
	Headers map[string]string
	Timeout time.Duration
	// MaxRetries bounds transport-level retries of the initial request
	// (default 0). Failures mid-stream are not retried at this layer.
	MaxRetries int
	// MaxRetryDelay caps server-requested retry delays. Zero means the 60s
	// default; negative disables the cap.
	MaxRetryDelay time.Duration
	// Env holds request-scoped environment overrides that take precedence
	// over the process environment for provider configuration.
	Env        map[string]string
	Reasoning  ThinkingLevel
	ToolChoice any

	// OnPayload may inspect or replace the request body before sending. Return
	// nil to keep the payload unchanged.
	OnPayload func(payload map[string]any) map[string]any
	// OnResponse is called with the HTTP status and headers before the body is read.
	OnResponse func(status int, headers map[string]string)

	reasoningEffort ThinkingLevel
}

const (
	contextSafetyTokens = 4096
	minMaxTokens        = 1
)

// clampMaxTokensToContext bounds maxTokens so the request fits the model's
// context window, leaving a safety margin.
func clampMaxTokensToContext(m *Model, ctx *Context, maxTokens int) int {
	if m.ContextWindow <= 0 {
		return max(minMaxTokens, maxTokens)
	}
	available := m.ContextWindow - EstimateContextTokens(ctx).Tokens - contextSafetyTokens
	return min(maxTokens, max(minMaxTokens, available))
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
		o.APIKey = envAPIKey(model.provider(), o.Env)
	}
	maxTokens := o.MaxTokens
	if maxTokens == 0 {
		maxTokens = model.MaxTokens
	}
	o.MaxTokens = clampMaxTokensToContext(model, reqCtx, maxTokens)
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

func resolveCacheRetention(r CacheRetention, env map[string]string) CacheRetention {
	if r != "" {
		return r
	}
	if providerEnvValue("PI_CACHE_RETENTION", env) == "long" {
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
					tc.customProp = ""
					tc.customBuf = nil
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

func hasHeaderValue(headers map[string]string, name string) bool {
	for k, v := range headers {
		if strings.EqualFold(k, name) && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// clientAPIKey resolves the request API key. A caller-supplied Authorization
// (or Cloudflare gateway auth) header stands in for a key.
func clientAPIKey(provider string, opts *StreamOptions) (string, error) {
	if opts.APIKey != "" {
		return opts.APIKey, nil
	}
	if hasHeaderValue(opts.Headers, "authorization") || hasHeaderValue(opts.Headers, "cf-aig-authorization") {
		return "unused", nil
	}
	return "", fmt.Errorf("No API key for provider: %s", provider)
}

// requestHeaders merges provider defaults, session-affinity headers and
// caller overrides. An empty caller value suppresses the header entirely.
func requestHeaders(model *Model, opts *StreamOptions, compat resolvedCompat, retention CacheRetention) map[string]string {
	headers := make(map[string]string, len(model.Headers)+len(opts.Headers)+3)
	for k, v := range model.Headers {
		headers[k] = v
	}
	if opts.SessionID != "" && compat.sendSessionAffinityHeaders && retention != CacheNone {
		if compat.sessionAffinityFormat == SessionAffinityOpenRouter {
			headers["x-session-id"] = opts.SessionID
		} else {
			if compat.sessionAffinityFormat == SessionAffinityOpenAI {
				headers["session_id"] = opts.SessionID
			}
			headers["x-client-request-id"] = opts.SessionID
			headers["x-session-affinity"] = opts.SessionID
		}
	}
	for k, v := range opts.Headers {
		headers[k] = v
	}
	return headers
}

func runCompletion(ctx context.Context, model *Model, reqCtx *Context, opts *StreamOptions, stream *Stream, output *AssistantMessage) error {
	apiKey, err := clientAPIKey(model.provider(), opts)
	if err != nil {
		return err
	}

	compat := getCompat(model)
	grammarProps, err := grammarInputProperties(reqCtx.Tools, compat.supportsOpenAIGrammarTools)
	if err != nil {
		return err
	}
	retention := resolveCacheRetention(opts.CacheRetention, opts.Env)
	params, err := buildParams(model, reqCtx, opts, compat, retention, grammarProps)
	if err != nil {
		return err
	}

	if opts.OnPayload != nil {
		if replaced := opts.OnPayload(params); replaced != nil {
			params = replaced
		}
	}

	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	headers := requestHeaders(model, opts, compat, retention)

	client := &http.Client{}
	if opts.Timeout > 0 {
		client.Timeout = opts.Timeout
	}

	resp, err := retryProviderRequest(ctx, func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, model.baseURL()+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		for k, v := range headers {
			if v == "" {
				req.Header.Del(k)
			} else {
				req.Header.Set(k, v)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			defer resp.Body.Close()
			return nil, httpError(resp)
		}
		return resp, nil
	}, opts.MaxRetries, opts.MaxRetryDelay)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if opts.OnResponse != nil {
		respHeaders := make(map[string]string, len(resp.Header))
		for k := range resp.Header {
			respHeaders[k] = resp.Header.Get(k)
		}
		opts.OnResponse(resp.StatusCode, respHeaders)
	}

	stream.push(StartEvent{baseEvent{output}})
	return consumeSSE(ctx, resp.Body, model, grammarProps, stream, output)
}

const maxProviderErrorBodyChars = 4000

func truncateErrorText(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return fmt.Sprintf("%s... [truncated %d chars]", text[:maxChars], len(text)-maxChars)
}

// httpError formats a non-2xx provider response the way pi's
// normalizeProviderError/formatProviderError render OpenAI SDK errors:
// "<status>: <error body JSON>" when the body carries an error object with a
// message, the SDK-style "<status> <body>" otherwise.
func httpError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	status := resp.StatusCode

	var message string
	var errField json.RawMessage
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &parsed) == nil {
		errField = parsed.Error
	}

	switch {
	case len(errField) > 0 && errField[0] == '{' && string(errField) != "{}":
		var buf bytes.Buffer
		if json.Compact(&buf, errField) != nil {
			buf.Reset()
			buf.Write(errField)
		}
		bodyJSON := truncateErrorText(buf.String(), maxProviderErrorBodyChars)
		var inner struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(errField, &inner)
		if inner.Message != "" {
			message = fmt.Sprintf("%d: %s", status, bodyJSON)
		} else {
			message = fmt.Sprintf("%d %s", status, bodyJSON)
		}
		// Some providers via OpenRouter give additional information in this
		// field; only append it when the body did not already surface it.
		if inner.Metadata.Raw != "" && !strings.Contains(message, inner.Metadata.Raw) {
			message += "\n" + inner.Metadata.Raw
		}
	case len(errField) > 0 && string(errField) != "null":
		message = fmt.Sprintf("%d %s", status, string(errField))
	case strings.TrimSpace(string(data)) != "":
		message = fmt.Sprintf("%d %s", status, string(data))
	default:
		message = fmt.Sprintf("%d status code (no body)", status)
	}

	return &providerHTTPError{status: status, headers: resp.Header, message: message}
}

func buildParams(model *Model, reqCtx *Context, opts *StreamOptions, compat resolvedCompat, retention CacheRetention, grammarProps map[string]string) (map[string]any, error) {
	messages, err := convertMessages(model, reqCtx, compat, grammarProps)
	if err != nil {
		return nil, err
	}

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

	deferred := map[string]bool{}
	if compat.deferredToolsMode == DeferredToolsKimi {
		deferred = deferredToolNames(reqCtx.Messages)
	}
	var activeTools []Tool
	for _, t := range reqCtx.Tools {
		if !deferred[t.Name] {
			activeTools = append(activeTools, t)
		}
	}

	var tools []map[string]any
	if len(activeTools) > 0 {
		tools, err = convertTools(activeTools, compat)
		if err != nil {
			return nil, err
		}
		params["tools"] = tools
		if compat.zaiToolStream {
			params["tool_stream"] = true
		}
	} else if hasToolHistory(reqCtx.Messages) {
		// Anthropic (via LiteLLM/proxy) requires the tools param when the
		// conversation has tool_calls/tool_results.
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

	if compat.openRouterRouting != nil {
		params["provider"] = compat.openRouterRouting
	}
	if routing := compat.vercelGatewayRouting; routing != nil && (len(routing.Only) > 0 || len(routing.Order) > 0) {
		gateway := map[string]any{}
		if len(routing.Only) > 0 {
			gateway["only"] = routing.Only
		}
		if len(routing.Order) > 0 {
			gateway["order"] = routing.Order
		}
		params["providerOptions"] = map[string]any{"gateway": gateway}
	}

	return params, nil
}

func resolveChatTemplateKwarg(m *Model, effort ThinkingLevel, kw ChatTemplateKwarg) (any, bool) {
	if kw.Var == "" {
		return kw.Value, true
	}
	if effort == "" && kw.OmitWhenOff {
		return nil, false
	}
	if kw.Var == "thinking.enabled" {
		return effort != "", true
	}

	level := effort
	if effort == "" {
		level = ThinkingOff
	}
	if m.NullLevels[level] {
		return nil, false
	}
	if v, ok := m.ThinkingLevelMap[level]; ok {
		return v, true
	}
	if effort == "" {
		return nil, false
	}
	return string(effort), true
}

func buildChatTemplateKwargs(m *Model, effort ThinkingLevel, compat resolvedCompat) map[string]any {
	kwargs := map[string]any{}
	for key, kw := range compat.chatTemplateKwargs {
		if v, ok := resolveChatTemplateKwarg(m, effort, kw); ok {
			kwargs[key] = v
		}
	}
	if len(kwargs) == 0 {
		return nil
	}
	return kwargs
}

func applyReasoning(model *Model, opts *StreamOptions, compat resolvedCompat, params map[string]any) {
	if !model.Reasoning {
		return
	}
	effort := opts.reasoningEffort
	on := effort != ""
	// mapped mirrors TS `thinkingLevelMap?.[level] ?? level`: both a missing
	// and an explicit null entry fall back to the level itself.
	mapped := func(level ThinkingLevel) string {
		if v, ok := model.ThinkingLevelMap[level]; ok {
			return v
		}
		return string(level)
	}

	switch compat.thinkingFormat {
	case ThinkingFormatZAI:
		if on {
			params["thinking"] = map[string]any{"type": "enabled", "clear_thinking": false}
		} else {
			params["thinking"] = map[string]any{"type": "disabled"}
		}
		if on && compat.supportsReasoningEffort && !model.NullLevels[effort] {
			params["reasoning_effort"] = mapped(effort)
		}
	case ThinkingFormatQwen:
		params["enable_thinking"] = on
	case ThinkingFormatQwenChatTemplate:
		params["chat_template_kwargs"] = map[string]any{
			"enable_thinking":   on,
			"preserve_thinking": true,
		}
	case ThinkingFormatChatTemplate:
		if kwargs := buildChatTemplateKwargs(model, effort, compat); kwargs != nil {
			params["chat_template_kwargs"] = kwargs
		}
	case ThinkingFormatDeepSeek:
		if on {
			params["thinking"] = map[string]any{"type": "enabled"}
		} else if !model.NullLevels[ThinkingOff] {
			params["thinking"] = map[string]any{"type": "disabled"}
		}
		if on && compat.supportsReasoningEffort {
			params["reasoning_effort"] = mapped(effort)
		}
	case ThinkingFormatOpenRouter:
		// OpenRouter normalizes reasoning across providers via a nested
		// reasoning object.
		if on {
			params["reasoning"] = map[string]any{"effort": mapped(effort)}
		} else if !model.NullLevels[ThinkingOff] {
			offVal := "none"
			if v, ok := model.ThinkingLevelMap[ThinkingOff]; ok {
				offVal = v
			}
			params["reasoning"] = map[string]any{"effort": offVal}
		}
	case ThinkingFormatAntLing:
		// Sent only when the model maps this effort to an explicit value.
		if on {
			if v, ok := model.ThinkingLevelMap[effort]; ok {
				params["reasoning"] = map[string]any{"effort": v}
			}
		}
	case ThinkingFormatTogether:
		params["reasoning"] = map[string]any{"enabled": on}
		if on && compat.supportsReasoningEffort {
			params["reasoning_effort"] = mapped(effort)
		}
	case ThinkingFormatStringThinking:
		if on {
			params["thinking"] = mapped(effort)
		} else if !model.NullLevels[ThinkingOff] {
			offVal := "none"
			if v, ok := model.ThinkingLevelMap[ThinkingOff]; ok {
				offVal = v
			}
			params["thinking"] = offVal
		}
	default:
		if !compat.supportsReasoningEffort {
			return
		}
		if on {
			params["reasoning_effort"] = mapped(effort)
		} else if v, ok := model.ThinkingLevelMap[ThinkingOff]; ok {
			params["reasoning_effort"] = v
		}
	}
}
