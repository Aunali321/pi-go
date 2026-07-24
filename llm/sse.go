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
	Index    *int       `json:"index"`
	ID       string     `json:"id"`
	Function *sseFunc   `json:"function"`
	Custom   *sseCustom `json:"custom"`
}

type sseFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type sseCustom struct {
	Name  string `json:"name"`
	Input string `json:"input"`
}

type rawUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails  *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// sseState carries the per-stream parsing state shared between consumeSSE and
// handleChunk.
type sseState struct {
	model        *Model
	stream       *Stream
	output       *AssistantMessage
	grammarProps map[string]string

	textBlock     *Text
	thinkingBlock *Thinking
	hasFinish     bool
	byIndex       map[int]*ToolCall
	byID          map[string]*ToolCall
	// pendingSignatures buffers encrypted reasoning details whose tool call
	// has not streamed yet, keyed by tool call id.
	pendingSignatures map[string]string
}

func (s *sseState) indexOf(target Content) int {
	for i, c := range s.output.Content {
		if c == target {
			return i
		}
	}
	return -1
}

func (s *sseState) ensureText() *Text {
	if s.textBlock == nil {
		s.textBlock = &Text{}
		s.output.Content = append(s.output.Content, s.textBlock)
		s.stream.push(TextStartEvent{baseEvent{s.output}, s.indexOf(s.textBlock)})
	}
	return s.textBlock
}

func (s *sseState) ensureThinking(sig string) *Thinking {
	if s.thinkingBlock == nil {
		s.thinkingBlock = &Thinking{Signature: sig}
		s.output.Content = append(s.output.Content, s.thinkingBlock)
		s.stream.push(ThinkingStartEvent{baseEvent{s.output}, s.indexOf(s.thinkingBlock)})
	}
	return s.thinkingBlock
}

func (s *sseState) applyPendingSignature(block *ToolCall) {
	if block.ID == "" {
		return
	}
	if sig, ok := s.pendingSignatures[block.ID]; ok {
		block.ThoughtSignature = sig
		delete(s.pendingSignatures, block.ID)
	}
}

func (s *sseState) initCustomInput(block *ToolCall) {
	prop, ok := s.grammarProps[block.Name]
	if !ok {
		// The model called a grammar tool we do not know about; stash the
		// input under a fallback property so it is not lost.
		prop = "input"
	}
	block.Arguments = map[string]any{prop: ""}
	block.customProp = prop
	block.customBuf = &grammarInputBuffer{}
	block.partialArgs = ""
}

func (s *sseState) ensureToolCall(tc sseToolCall) *ToolCall {
	var block *ToolCall
	if tc.Index != nil {
		block = s.byIndex[*tc.Index]
	}
	if block == nil && tc.ID != "" {
		block = s.byID[tc.ID]
	}
	name := ""
	if tc.Function != nil {
		name = tc.Function.Name
	}
	if name == "" && tc.Custom != nil {
		name = tc.Custom.Name
	}
	if block == nil {
		block = &ToolCall{ID: tc.ID, Name: name, Arguments: map[string]any{}}
		if tc.Custom != nil {
			s.initCustomInput(block)
		}
		if tc.Index != nil {
			block.streamIndex = *tc.Index
			block.hasIndex = true
			s.byIndex[*tc.Index] = block
		}
		s.output.Content = append(s.output.Content, block)
		s.stream.push(ToolCallStartEvent{baseEvent{s.output}, s.indexOf(block)})
	}
	if tc.Index != nil && !block.hasIndex {
		block.streamIndex = *tc.Index
		block.hasIndex = true
		s.byIndex[*tc.Index] = block
	}
	if tc.ID != "" {
		s.byID[tc.ID] = block
	}
	if block.Name == "" && name != "" {
		block.Name = name
	}
	if tc.Custom != nil && block.customBuf == nil {
		s.initCustomInput(block)
	}
	s.applyPendingSignature(block)
	return block
}

// appendCustomInput advances a grammar tool call to nextInput and returns the
// synthesized JSON delta (ok reports whether one was produced).
func appendCustomInput(block *ToolCall, nextInput string, close bool) (string, bool, error) {
	if block.customBuf == nil {
		return "", false, nil
	}
	delta, ok, err := appendGrammarInputDelta(block.customBuf, block.customProp, nextInput, close)
	if err != nil {
		return "", false, err
	}
	block.Arguments = map[string]any{block.customProp: nextInput}
	return delta, ok, nil
}

func consumeSSE(ctx context.Context, body io.Reader, model *Model, grammarProps map[string]string, stream *Stream, output *AssistantMessage) error {
	state := &sseState{
		model:             model,
		stream:            stream,
		output:            output,
		grammarProps:      grammarProps,
		byIndex:           map[int]*ToolCall{},
		byID:              map[string]*ToolCall{},
		pendingSignatures: map[string]string{},
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
						if err := handleChunk(&chunk, state); err != nil {
							return err
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
		if err := finishBlock(c, state); err != nil {
			return err
		}
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
	if !state.hasFinish {
		return fmt.Errorf("stream ended without finish_reason")
	}

	stream.push(DoneEvent{baseEvent{output}, output.StopReason, output})
	return nil
}

func handleChunk(chunk *sseChunk, s *sseState) error {
	output := s.output
	if output.ResponseID == "" {
		output.ResponseID = chunk.ID
	}
	if chunk.Model != "" && chunk.Model != s.model.ID && output.ResponseModel == "" {
		output.ResponseModel = chunk.Model
	}
	if chunk.Usage != nil {
		output.Usage = parseUsage(chunk.Usage, s.model)
	}
	if len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]
	if chunk.Usage == nil && choice.Usage != nil {
		output.Usage = parseUsage(choice.Usage, s.model)
	}

	if choice.FinishReason != nil {
		reason, errMsg := mapStopReason(*choice.FinishReason)
		output.StopReason = reason
		if errMsg != "" {
			output.ErrorMessage = errMsg
		}
		s.hasFinish = true
	}

	d := choice.Delta
	if d.Content != nil && *d.Content != "" {
		block := s.ensureText()
		block.Text += *d.Content
		s.stream.push(TextDeltaEvent{baseEvent{output}, s.indexOf(block), *d.Content})
	}

	reasoning, field := firstReasoning(d)
	if reasoning != "" {
		sig := field
		if s.model.provider() == "opencode-go" && field == "reasoning" {
			sig = "reasoning_content"
		}
		block := s.ensureThinking(sig)
		block.Thinking += reasoning
		s.stream.push(ThinkingDeltaEvent{baseEvent{output}, s.indexOf(block), reasoning})
	}

	for _, tc := range d.ToolCalls {
		block := s.ensureToolCall(tc)
		if block.ID == "" && tc.ID != "" {
			block.ID = tc.ID
			s.byID[tc.ID] = block
		}
		delta := ""
		if tc.Function != nil && tc.Function.Arguments != "" {
			delta = tc.Function.Arguments
			block.partialArgs += tc.Function.Arguments
			block.Arguments = parseStreamingJSON(block.partialArgs)
		} else if tc.Custom != nil && tc.Custom.Input != "" {
			next := block.customBuf.input + tc.Custom.Input
			d, ok, err := appendCustomInput(block, next, false)
			if err != nil {
				return err
			}
			if ok {
				delta = d
			}
		}
		s.stream.push(ToolCallDeltaEvent{baseEvent{output}, s.indexOf(block), delta})
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
		// Data must be a non-empty JSON string, matching the TS type guard.
		if head.Type == "reasoning.encrypted" && head.ID != "" && len(head.Data) > 2 && head.Data[0] == '"' {
			if block, ok := s.byID[head.ID]; ok {
				block.ThoughtSignature = string(raw)
			} else {
				s.pendingSignatures[head.ID] = string(raw)
			}
		}
	}

	return nil
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

func finishBlock(block Content, s *sseState) error {
	idx := s.indexOf(block)
	if idx == -1 {
		return nil
	}
	switch b := block.(type) {
	case *Text:
		s.stream.push(TextEndEvent{baseEvent{s.output}, idx, b.Text})
	case *Thinking:
		s.stream.push(ThinkingEndEvent{baseEvent{s.output}, idx, b.Thinking})
	case *ToolCall:
		if b.customBuf != nil {
			delta, ok, err := appendCustomInput(b, b.customBuf.input, true)
			if err != nil {
				return err
			}
			if ok {
				s.stream.push(ToolCallDeltaEvent{baseEvent{s.output}, idx, delta})
			}
		} else {
			b.Arguments = parseStreamingJSON(b.partialArgs)
		}
		// Finalize in-place and strip the scratch buffers so replay only
		// carries parsed arguments.
		b.partialArgs = ""
		b.hasIndex = false
		b.customProp = ""
		b.customBuf = nil
		s.stream.push(ToolCallEndEvent{baseEvent{s.output}, idx, b})
	}
	return nil
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
	reasoning := 0
	if raw.CompletionTokensDetails != nil {
		reasoning = raw.CompletionTokensDetails.ReasoningTokens
	}
	u := Usage{
		Input:       input,
		Output:      raw.CompletionTokens,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		Reasoning:   &reasoning,
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
