package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aunali321/pi-go/llm"
)

type toolBatch struct {
	messages  []*llm.ToolResultMessage
	terminate bool
}

type finalized struct {
	toolCall *llm.ToolCall
	result   ToolResult
	isError  bool
}

type preparation struct {
	immediate bool
	result    ToolResult
	isError   bool

	toolCall *llm.ToolCall
	tool     Tool
	args     map[string]any
}

func executeToolCalls(ctx context.Context, agentCtx *Context, assistant *llm.AssistantMessage, cfg *Config, emit EventSink) toolBatch {
	tcs := assistantToolCalls(assistant)

	hasSequential := false
	for _, tc := range tcs {
		if t := findTool(agentCtx.Tools, tc.Name); t != nil && t.Mode() == ModeSequential {
			hasSequential = true
			break
		}
	}

	if cfg.ToolExecution == ModeSequential || hasSequential {
		return executeSequential(ctx, agentCtx, assistant, tcs, cfg, emit)
	}
	return executeParallel(ctx, agentCtx, assistant, tcs, cfg, emit)
}

func executeSequential(ctx context.Context, agentCtx *Context, assistant *llm.AssistantMessage, tcs []*llm.ToolCall, cfg *Config, emit EventSink) toolBatch {
	var all []finalized
	var messages []*llm.ToolResultMessage

	for _, tc := range tcs {
		emit(ToolExecutionStart{tc.ID, tc.Name, tc.Arguments})

		p := prepareToolCall(ctx, agentCtx, assistant, tc, cfg)
		var f finalized
		if p.immediate {
			f = finalized{tc, p.result, p.isError}
		} else {
			res, isErr := executePrepared(ctx, p, emit)
			f = finalizeToolCall(ctx, agentCtx, assistant, p, res, isErr, cfg)
		}

		emit(ToolExecutionEnd{f.toolCall.ID, f.toolCall.Name, f.result, f.isError})
		trm := toolResultMessage(f)
		emit(MessageStart{trm})
		emit(MessageEnd{trm})

		all = append(all, f)
		messages = append(messages, trm)

		if ctx.Err() != nil {
			break
		}
	}

	return toolBatch{messages, shouldTerminate(all)}
}

func executeParallel(ctx context.Context, agentCtx *Context, assistant *llm.AssistantMessage, tcs []*llm.ToolCall, cfg *Config, emit EventSink) toolBatch {
	var mu sync.Mutex
	safeEmit := func(e Event) {
		mu.Lock()
		emit(e)
		mu.Unlock()
	}

	type slot struct {
		immediate bool
		f         finalized
		run       func() finalized
	}
	var slots []slot

	for _, tc := range tcs {
		emit(ToolExecutionStart{tc.ID, tc.Name, tc.Arguments})

		p := prepareToolCall(ctx, agentCtx, assistant, tc, cfg)
		if p.immediate {
			f := finalized{tc, p.result, p.isError}
			emit(ToolExecutionEnd{f.toolCall.ID, f.toolCall.Name, f.result, f.isError})
			slots = append(slots, slot{immediate: true, f: f})
			if ctx.Err() != nil {
				break
			}
			continue
		}

		pc := p
		slots = append(slots, slot{run: func() finalized {
			res, isErr := executePrepared(ctx, pc, safeEmit)
			f := finalizeToolCall(ctx, agentCtx, assistant, pc, res, isErr, cfg)
			safeEmit(ToolExecutionEnd{f.toolCall.ID, f.toolCall.Name, f.result, f.isError})
			return f
		}})
		if ctx.Err() != nil {
			break
		}
	}

	results := make([]finalized, len(slots))
	var wg sync.WaitGroup
	for i, s := range slots {
		if s.immediate {
			results[i] = s.f
			continue
		}
		wg.Add(1)
		go func(i int, run func() finalized) {
			defer wg.Done()
			results[i] = run()
		}(i, s.run)
	}
	wg.Wait()

	messages := make([]*llm.ToolResultMessage, 0, len(results))
	for _, f := range results {
		trm := toolResultMessage(f)
		emit(MessageStart{trm})
		emit(MessageEnd{trm})
		messages = append(messages, trm)
	}

	return toolBatch{messages, shouldTerminate(results)}
}

func prepareToolCall(ctx context.Context, agentCtx *Context, assistant *llm.AssistantMessage, tc *llm.ToolCall, cfg *Config) preparation {
	tool := findTool(agentCtx.Tools, tc.Name)
	if tool == nil {
		return preparation{immediate: true, result: textResult("Tool " + tc.Name + " not found"), isError: true}
	}

	if cfg.BeforeToolCall != nil {
		res := cfg.BeforeToolCall(ctx, BeforeToolCall{
			Assistant: assistant,
			ToolCall:  tc,
			Args:      tc.Arguments,
			Context:   agentCtx,
		})
		if ctx.Err() != nil {
			return preparation{immediate: true, result: textResult("Operation aborted"), isError: true}
		}
		if res.Block {
			reason := res.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			return preparation{immediate: true, result: textResult(reason), isError: true}
		}
	}

	if ctx.Err() != nil {
		return preparation{immediate: true, result: textResult("Operation aborted"), isError: true}
	}

	return preparation{toolCall: tc, tool: tool, args: tc.Arguments}
}

func executePrepared(ctx context.Context, p preparation, emit EventSink) (result ToolResult, isError bool) {
	defer func() {
		if r := recover(); r != nil {
			result = textResult(fmt.Sprintf("tool %q panicked: %v", p.toolCall.Name, r))
			isError = true
		}
	}()

	raw, _ := json.Marshal(p.toolCall.Arguments)
	res, err := p.tool.Execute(ctx, p.toolCall.ID, raw, func(partial ToolResult) {
		emit(ToolExecutionUpdate{p.toolCall.ID, p.toolCall.Name, p.toolCall.Arguments, partial})
	})
	if err != nil {
		return textResult(err.Error()), true
	}
	return res, false
}

func finalizeToolCall(ctx context.Context, agentCtx *Context, assistant *llm.AssistantMessage, p preparation, result ToolResult, isError bool, cfg *Config) finalized {
	if cfg.AfterToolCall != nil {
		after := cfg.AfterToolCall(ctx, AfterToolCall{
			Assistant: assistant,
			ToolCall:  p.toolCall,
			Args:      p.args,
			Result:    result,
			IsError:   isError,
			Context:   agentCtx,
		})
		if after != nil {
			if after.Content != nil {
				result.Content = after.Content
			}
			if after.Details != nil {
				result.Details = after.Details
			}
			if after.Terminate != nil {
				result.Terminate = *after.Terminate
			}
			if after.IsError != nil {
				isError = *after.IsError
			}
		}
	}
	return finalized{p.toolCall, result, isError}
}

func toolResultMessage(f finalized) *llm.ToolResultMessage {
	return &llm.ToolResultMessage{
		ToolCallID: f.toolCall.ID,
		ToolName:   f.toolCall.Name,
		Content:    f.result.Content,
		Details:    f.result.Details,
		IsError:    f.isError,
		Timestamp:  time.Now(),
	}
}

func shouldTerminate(all []finalized) bool {
	if len(all) == 0 {
		return false
	}
	for _, f := range all {
		if !f.result.Terminate {
			return false
		}
	}
	return true
}
