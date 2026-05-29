package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type sseChunk struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Choices []sseChoice `json:"choices"`
	Usage   *rawUsage   `json:"usage"`
}

type sseChoice struct {
	Delta        sseDelta  `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
	Usage        *rawUsage `json:"usage"`
}

type sseDelta struct {
	Content          *string           `json:"content"`
	ReasoningContent string            `json:"reasoning_content"`
	Reasoning        string            `json:"reasoning"`
	ReasoningText    string            `json:"reasoning_text"`
	ToolCalls        []sseToolCall     `json:"tool_calls"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details"`
}

type sseToolCall struct {
	Index    *int     `json:"index"`
	ID       string   `json:"id"`
	Function *sseFunc `json:"function"`
}

type sseFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type rawUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

func consumeSSE(ctx context.Context, body io.Reader, model *Model, stream *Stream, output *AssistantMessage) error {
	var textBlock *Text
	var thinkingBlock *Thinking
	hasFinish := false
	byIndex := map[int]*ToolCall{}
	byID := map[string]*ToolCall{}

	indexOf := func(target Content) int {
		for i, c := range output.Content {
			if c == target {
				return i
			}
		}
		return -1
	}

	ensureText := func() *Text {
		if textBlock == nil {
			textBlock = &Text{}
			output.Content = append(output.Content, textBlock)
			stream.push(TextStartEvent{baseEvent{output}, indexOf(textBlock)})
		}
		return textBlock
	}
	ensureThinking := func(sig string) *Thinking {
		if thinkingBlock == nil {
			thinkingBlock = &Thinking{Signature: sig}
			output.Content = append(output.Content, thinkingBlock)
			stream.push(ThinkingStartEvent{baseEvent{output}, indexOf(thinkingBlock)})
		}
		return thinkingBlock
	}
	ensureToolCall := func(tc sseToolCall) *ToolCall {
		var block *ToolCall
		if tc.Index != nil {
			block = byIndex[*tc.Index]
		}
		if block == nil && tc.ID != "" {
			block = byID[tc.ID]
		}
		if block == nil {
			block = &ToolCall{Arguments: map[string]any{}}
			if tc.ID != "" {
				block.ID = tc.ID
			}
			if tc.Function != nil {
				block.Name = tc.Function.Name
			}
			if tc.Index != nil {
				block.streamIndex = *tc.Index
				block.hasIndex = true
				byIndex[*tc.Index] = block
			}
			if tc.ID != "" {
				byID[tc.ID] = block
			}
			output.Content = append(output.Content, block)
			stream.push(ToolCallStartEvent{baseEvent{output}, indexOf(block)})
		}
		if tc.Index != nil && !block.hasIndex {
			block.streamIndex = *tc.Index
			block.hasIndex = true
			byIndex[*tc.Index] = block
		}
		if tc.ID != "" {
			byID[tc.ID] = block
		}
		return block
	}

	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
			if ok {
				data = strings.TrimSpace(data)
				if data == "[DONE]" {
					break
				}
				if data != "" {
					var chunk sseChunk
					if json.Unmarshal([]byte(data), &chunk) == nil {
						if done := handleChunk(&chunk, model, stream, output, ensureText, ensureThinking, ensureToolCall, indexOf); done {
							hasFinish = true
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	for _, c := range output.Content {
		finishBlock(c, stream, output, indexOf)
	}

	if ctx.Err() != nil {
		return fmt.Errorf("request was aborted")
	}
	switch output.StopReason {
	case StopAborted:
		return fmt.Errorf("request was aborted")
	case StopError:
		if output.ErrorMessage != "" {
			return fmt.Errorf("%s", output.ErrorMessage)
		}
		return fmt.Errorf("provider returned an error stop reason")
	}
	if !hasFinish {
		return fmt.Errorf("stream ended without finish_reason")
	}

	stream.push(DoneEvent{baseEvent{output}, output.StopReason, output})
	return nil
}

func handleChunk(
	chunk *sseChunk,
	model *Model,
	stream *Stream,
	output *AssistantMessage,
	ensureText func() *Text,
	ensureThinking func(string) *Thinking,
	ensureToolCall func(sseToolCall) *ToolCall,
	indexOf func(Content) int,
) (hasFinish bool) {
	if output.ResponseID == "" {
		output.ResponseID = chunk.ID
	}
	if chunk.Model != "" && chunk.Model != model.ID && output.ResponseModel == "" {
		output.ResponseModel = chunk.Model
	}
	if chunk.Usage != nil {
		output.Usage = parseUsage(chunk.Usage, model)
	}
	if len(chunk.Choices) == 0 {
		return false
	}
	choice := chunk.Choices[0]
	if chunk.Usage == nil && choice.Usage != nil {
		output.Usage = parseUsage(choice.Usage, model)
	}

	if choice.FinishReason != nil {
		reason, errMsg := mapStopReason(*choice.FinishReason)
		output.StopReason = reason
		if errMsg != "" {
			output.ErrorMessage = errMsg
		}
		hasFinish = true
	}

	d := choice.Delta
	if d.Content != nil && *d.Content != "" {
		block := ensureText()
		block.Text += *d.Content
		stream.push(TextDeltaEvent{baseEvent{output}, indexOf(block), *d.Content})
	}

	reasoning, field := firstReasoning(d)
	if reasoning != "" {
		sig := field
		if model.provider() == "opencode-go" && field == "reasoning" {
			sig = "reasoning_content"
		}
		block := ensureThinking(sig)
		block.Thinking += reasoning
		stream.push(ThinkingDeltaEvent{baseEvent{output}, indexOf(block), reasoning})
	}

	for _, tc := range d.ToolCalls {
		block := ensureToolCall(tc)
		if block.ID == "" && tc.ID != "" {
			block.ID = tc.ID
		}
		if block.Name == "" && tc.Function != nil && tc.Function.Name != "" {
			block.Name = tc.Function.Name
		}
		delta := ""
		if tc.Function != nil && tc.Function.Arguments != "" {
			delta = tc.Function.Arguments
			block.partialArgs += tc.Function.Arguments
			block.Arguments = parseStreamingJSON(block.partialArgs)
		}
		stream.push(ToolCallDeltaEvent{baseEvent{output}, indexOf(block), delta})
	}

	for _, raw := range d.ReasoningDetails {
		var head struct {
			Type string          `json:"type"`
			ID   string          `json:"id"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &head) != nil {
			continue
		}
		if head.Type == "reasoning.encrypted" && head.ID != "" && len(head.Data) > 0 && string(head.Data) != "null" {
			for _, c := range output.Content {
				if tc, ok := c.(*ToolCall); ok && tc.ID == head.ID {
					tc.ThoughtSignature = string(raw)
					break
				}
			}
		}
	}

	return hasFinish
}

func firstReasoning(d sseDelta) (text, field string) {
	switch {
	case d.ReasoningContent != "":
		return d.ReasoningContent, "reasoning_content"
	case d.Reasoning != "":
		return d.Reasoning, "reasoning"
	case d.ReasoningText != "":
		return d.ReasoningText, "reasoning_text"
	}
	return "", ""
}

func finishBlock(block Content, stream *Stream, output *AssistantMessage, indexOf func(Content) int) {
	idx := indexOf(block)
	if idx == -1 {
		return
	}
	switch b := block.(type) {
	case *Text:
		stream.push(TextEndEvent{baseEvent{output}, idx, b.Text})
	case *Thinking:
		stream.push(ThinkingEndEvent{baseEvent{output}, idx, b.Thinking})
	case *ToolCall:
		b.Arguments = parseStreamingJSON(b.partialArgs)
		b.partialArgs = ""
		b.hasIndex = false
		stream.push(ToolCallEndEvent{baseEvent{output}, idx, b})
	}
}

func parseUsage(raw *rawUsage, model *Model) Usage {
	cacheRead := raw.PromptCacheHitTokens
	cacheWrite := 0
	if raw.PromptTokensDetails != nil {
		if raw.PromptTokensDetails.CachedTokens != 0 {
			cacheRead = raw.PromptTokensDetails.CachedTokens
		}
		cacheWrite = raw.PromptTokensDetails.CacheWriteTokens
	}
	input := raw.PromptTokens - cacheRead - cacheWrite
	if input < 0 {
		input = 0
	}
	u := Usage{
		Input:       input,
		Output:      raw.CompletionTokens,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + raw.CompletionTokens + cacheRead + cacheWrite,
	}
	calculateCost(model, &u)
	return u
}

func mapStopReason(reason string) (StopReason, string) {
	switch reason {
	case "", "stop", "end":
		return StopEnd, ""
	case "length":
		return StopLength, ""
	case "function_call", "tool_calls":
		return StopToolUse, ""
	case "content_filter":
		return StopError, "Provider finish_reason: content_filter"
	case "network_error":
		return StopError, "Provider finish_reason: network_error"
	default:
		return StopError, "Provider finish_reason: " + reason
	}
}
