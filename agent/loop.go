package agent

import (
	"context"

	"github.com/aunali321/pi-go/llm"
)

// Run starts an agent run with new prompt messages, appending them to the
// context, and drives turns until the agent stops. It returns the messages this
// run produced.
func Run(ctx context.Context, prompts []AgentMessage, agentCtx *Context, cfg *Config, emit EventSink) []AgentMessage {
	newMessages := append([]AgentMessage{}, prompts...)
	agentCtx.Messages = append(agentCtx.Messages, prompts...)

	emit(AgentStart{})
	emit(TurnStart{})
	for _, p := range prompts {
		emit(MessageStart{p})
		emit(MessageEnd{p})
	}

	runLoop(ctx, agentCtx, &newMessages, *cfg, emit)
	return newMessages
}

// Continue resumes a run from the current context without adding a prompt.
func Continue(ctx context.Context, agentCtx *Context, cfg *Config, emit EventSink) []AgentMessage {
	var newMessages []AgentMessage
	emit(AgentStart{})
	emit(TurnStart{})
	runLoop(ctx, agentCtx, &newMessages, *cfg, emit)
	return newMessages
}

func runLoop(ctx context.Context, agentCtx *Context, newMessages *[]AgentMessage, cfg Config, emit EventSink) {
	firstTurn := true
	var pending []AgentMessage
	if cfg.GetSteeringMessages != nil {
		pending = cfg.GetSteeringMessages()
	}

	for {
		hasMoreToolCalls := true

		for hasMoreToolCalls || len(pending) > 0 {
			if firstTurn {
				firstTurn = false
			} else {
				emit(TurnStart{})
			}

			if len(pending) > 0 {
				for _, m := range pending {
					emit(MessageStart{m})
					emit(MessageEnd{m})
					agentCtx.Messages = append(agentCtx.Messages, m)
					*newMessages = append(*newMessages, m)
				}
				pending = nil
			}

			msg := streamAssistant(ctx, agentCtx, &cfg, emit)
			*newMessages = append(*newMessages, msg)

			if msg.StopReason == llm.StopError || msg.StopReason == llm.StopAborted {
				emit(TurnEnd{Message: msg})
				emit(AgentEnd{*newMessages})
				return
			}

			tcs := assistantToolCalls(msg)
			var toolResults []*llm.ToolResultMessage
			hasMoreToolCalls = false
			if len(tcs) > 0 {
				batch := executeToolCalls(ctx, agentCtx, msg, &cfg, emit)
				toolResults = batch.messages
				hasMoreToolCalls = !batch.terminate
				for _, r := range toolResults {
					agentCtx.Messages = append(agentCtx.Messages, r)
					*newMessages = append(*newMessages, r)
				}
			}

			emit(TurnEnd{Message: msg, ToolResults: toolResults})

			info := TurnInfo{Message: msg, ToolResults: toolResults, Context: agentCtx, NewMessages: *newMessages}
			if cfg.PrepareNextTurn != nil {
				if upd := cfg.PrepareNextTurn(info); upd != nil {
					if upd.Context != nil {
						agentCtx = upd.Context
					}
					if upd.Model != nil {
						cfg.Model = upd.Model
					}
					if upd.Reasoning != nil {
						cfg.Reasoning = *upd.Reasoning
					}
				}
			}

			if cfg.ShouldStopAfterTurn != nil && cfg.ShouldStopAfterTurn(info) {
				emit(AgentEnd{*newMessages})
				return
			}

			pending = nil
			if cfg.GetSteeringMessages != nil {
				pending = cfg.GetSteeringMessages()
			}
		}

		var followUp []AgentMessage
		if cfg.GetFollowUpMessages != nil {
			followUp = cfg.GetFollowUpMessages()
		}
		if len(followUp) > 0 {
			pending = followUp
			continue
		}
		break
	}

	emit(AgentEnd{*newMessages})
}

func streamAssistant(ctx context.Context, agentCtx *Context, cfg *Config, emit EventSink) *llm.AssistantMessage {
	msgs := agentCtx.Messages
	if cfg.TransformContext != nil {
		msgs = cfg.TransformContext(ctx, msgs)
	}
	convert := cfg.ConvertToLLM
	if convert == nil {
		convert = defaultConvertToLLM
	}

	reqCtx := &llm.Context{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     convert(msgs),
		Tools:        toLLMTools(agentCtx.Tools),
	}

	opts := cfg.Options
	opts.Reasoning = cfg.Reasoning
	if cfg.APIKey != nil {
		if key := cfg.APIKey(cfg.Model.ResolvedProvider()); key != "" {
			opts.APIKey = key
		}
	}

	streamFn := cfg.Stream
	if streamFn == nil {
		streamFn = llm.StreamSimple
	}
	stream := streamFn(ctx, cfg.Model, reqCtx, &opts)

	addedPartial := false
	var partial *llm.AssistantMessage
	for ev := range stream.Events() {
		switch ev.(type) {
		case llm.StartEvent:
			partial = ev.Partial()
			agentCtx.Messages = append(agentCtx.Messages, partial)
			addedPartial = true
			emit(MessageStart{partial})
		case llm.DoneEvent, llm.ErrorEvent:
		default:
			if partial != nil {
				emit(MessageUpdate{Message: partial, Event: ev})
			}
		}
	}

	final := stream.Result()
	if addedPartial {
		agentCtx.Messages[len(agentCtx.Messages)-1] = final
	} else {
		agentCtx.Messages = append(agentCtx.Messages, final)
		emit(MessageStart{final})
	}
	emit(MessageEnd{final})
	return final
}

func assistantToolCalls(m *llm.AssistantMessage) []*llm.ToolCall {
	var out []*llm.ToolCall
	for _, c := range m.Content {
		if tc, ok := c.(*llm.ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}
