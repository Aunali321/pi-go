package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aunali321/pi-go/llm"
)

// QueueMode controls how queued messages are drained at a queue point.
type QueueMode string

const (
	QueueAll        QueueMode = "all"
	QueueOneAtATime QueueMode = "one-at-a-time"
)

// State is the observable agent state.
type State struct {
	SystemPrompt     string
	Model            *llm.Model
	ThinkingLevel    llm.ThinkingLevel
	Tools            []Tool
	Messages         []AgentMessage
	IsStreaming      bool
	StreamingMessage AgentMessage
	PendingToolCalls map[string]bool
	ErrorMessage     string
}

type messageQueue struct {
	messages []AgentMessage
	mode     QueueMode
}

func (q *messageQueue) enqueue(m AgentMessage) { q.messages = append(q.messages, m) }
func (q *messageQueue) hasItems() bool         { return len(q.messages) > 0 }
func (q *messageQueue) clear()                 { q.messages = nil }

func (q *messageQueue) drain() []AgentMessage {
	if q.mode == QueueAll {
		drained := q.messages
		q.messages = nil
		return drained
	}
	if len(q.messages) == 0 {
		return nil
	}
	first := q.messages[0]
	q.messages = q.messages[1:]
	return []AgentMessage{first}
}

// Listener observes agent events during a run.
type Listener func(event Event, ctx context.Context)

// AgentOptions configures an Agent.
type AgentOptions struct {
	SystemPrompt  string
	Model         *llm.Model
	ThinkingLevel llm.ThinkingLevel
	Tools         []Tool
	Messages      []AgentMessage

	ConvertToLLM     func([]AgentMessage) []llm.Message
	TransformContext func(ctx context.Context, msgs []AgentMessage) []AgentMessage
	Stream           StreamFunc
	APIKey           func(provider string) string
	Options          llm.StreamOptions
	SessionID        string

	BeforeToolCall  func(ctx context.Context, c BeforeToolCall) BeforeToolResult
	AfterToolCall   func(ctx context.Context, c AfterToolCall) *AfterToolResult
	PrepareNextTurn func(ctx context.Context) *TurnUpdate

	SteeringMode  QueueMode
	FollowUpMode  QueueMode
	ToolExecution ExecutionMode
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Agent is a stateful wrapper around the agent loop with queues, events and
// lifecycle management.
type Agent struct {
	mu    sync.Mutex
	state State

	listeners   map[int]Listener
	listenerSeq int
	steeringQ   messageQueue
	followUpQ   messageQueue
	active      *activeRun
	opts        AgentOptions
}

func NewAgent(opts AgentOptions) *Agent {
	if opts.SteeringMode == "" {
		opts.SteeringMode = QueueOneAtATime
	}
	if opts.FollowUpMode == "" {
		opts.FollowUpMode = QueueOneAtATime
	}
	if opts.ToolExecution == "" {
		opts.ToolExecution = ModeParallel
	}
	if opts.ThinkingLevel == "" {
		opts.ThinkingLevel = llm.ThinkingOff
	}
	if opts.SessionID != "" {
		opts.Options.SessionID = opts.SessionID
	}
	return &Agent{
		state: State{
			SystemPrompt:     opts.SystemPrompt,
			Model:            opts.Model,
			ThinkingLevel:    opts.ThinkingLevel,
			Tools:            append([]Tool{}, opts.Tools...),
			Messages:         append([]AgentMessage{}, opts.Messages...),
			PendingToolCalls: map[string]bool{},
		},
		listeners: map[int]Listener{},
		steeringQ: messageQueue{mode: opts.SteeringMode},
		followUpQ: messageQueue{mode: opts.FollowUpMode},
		opts:      opts,
	}
}

// Subscribe registers an event listener and returns an unsubscribe function.
func (a *Agent) Subscribe(l Listener) func() {
	a.mu.Lock()
	id := a.listenerSeq
	a.listenerSeq++
	a.listeners[id] = l
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		delete(a.listeners, id)
		a.mu.Unlock()
	}
}

// State returns a snapshot of the current state.
func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *Agent) Steer(m AgentMessage)    { a.steeringQ.enqueue(m) }
func (a *Agent) FollowUp(m AgentMessage) { a.followUpQ.enqueue(m) }
func (a *Agent) ClearSteeringQueue()     { a.steeringQ.clear() }
func (a *Agent) ClearFollowUpQueue()     { a.followUpQ.clear() }
func (a *Agent) ClearAllQueues()         { a.steeringQ.clear(); a.followUpQ.clear() }
func (a *Agent) HasQueuedMessages() bool { return a.steeringQ.hasItems() || a.followUpQ.hasItems() }

// SetMessages replaces the transcript (copied).
func (a *Agent) SetMessages(msgs []AgentMessage) {
	a.mu.Lock()
	a.state.Messages = append([]AgentMessage{}, msgs...)
	a.mu.Unlock()
}

// The following setters reconfigure the agent between runs; the next Prompt or
// Continue picks up the new values.

func (a *Agent) SetSystemPrompt(s string) { a.mu.Lock(); a.state.SystemPrompt = s; a.mu.Unlock() }
func (a *Agent) SetModel(m *llm.Model)    { a.mu.Lock(); a.state.Model = m; a.mu.Unlock() }
func (a *Agent) SetThinkingLevel(l llm.ThinkingLevel) {
	a.mu.Lock()
	a.state.ThinkingLevel = l
	a.mu.Unlock()
}
func (a *Agent) SetTools(tools []Tool) {
	a.mu.Lock()
	a.state.Tools = append([]Tool{}, tools...)
	a.mu.Unlock()
}
func (a *Agent) SetToolExecution(m ExecutionMode) {
	a.mu.Lock()
	a.opts.ToolExecution = m
	a.mu.Unlock()
}
func (a *Agent) SetBeforeToolCall(fn func(context.Context, BeforeToolCall) BeforeToolResult) {
	a.mu.Lock()
	a.opts.BeforeToolCall = fn
	a.mu.Unlock()
}
func (a *Agent) SetAfterToolCall(fn func(context.Context, AfterToolCall) *AfterToolResult) {
	a.mu.Lock()
	a.opts.AfterToolCall = fn
	a.mu.Unlock()
}

// SetSessionID sets the provider cache session id used for subsequent requests.
func (a *Agent) SetSessionID(id string) { a.mu.Lock(); a.opts.Options.SessionID = id; a.mu.Unlock() }

// SteeringMode / FollowUpMode getters and setters.
func (a *Agent) SteeringMode() QueueMode     { return a.steeringQ.mode }
func (a *Agent) SetSteeringMode(m QueueMode) { a.steeringQ.mode = m }
func (a *Agent) FollowUpMode() QueueMode     { return a.followUpQ.mode }
func (a *Agent) SetFollowUpMode(m QueueMode) { a.followUpQ.mode = m }

// Abort cancels the current run, if any.
func (a *Agent) Abort() {
	a.mu.Lock()
	run := a.active
	a.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

// WaitForIdle blocks until the current run completes.
func (a *Agent) WaitForIdle() {
	a.mu.Lock()
	run := a.active
	a.mu.Unlock()
	if run != nil {
		<-run.done
	}
}

// Reset clears transcript, runtime state and queues.
func (a *Agent) Reset() {
	a.mu.Lock()
	a.state.Messages = nil
	a.state.IsStreaming = false
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = map[string]bool{}
	a.state.ErrorMessage = ""
	a.mu.Unlock()
	a.steeringQ.clear()
	a.followUpQ.clear()
}

// Prompt starts a new run from text and optional images.
func (a *Agent) Prompt(ctx context.Context, text string, images ...*llm.Image) error {
	content := []llm.Content{&llm.Text{Text: text}}
	for _, img := range images {
		content = append(content, img)
	}
	msg := &llm.UserMessage{Content: content, Timestamp: time.Now()}
	return a.PromptMessages(ctx, []AgentMessage{msg})
}

// PromptMessages starts a new run from explicit messages.
func (a *Agent) PromptMessages(ctx context.Context, messages []AgentMessage) error {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return errors.New("agent is already processing a prompt")
	}
	a.mu.Unlock()
	return a.runWithLifecycle(ctx, func(runCtx context.Context) {
		Run(runCtx, messages, a.contextSnapshot(), a.loopConfig(false), a.processEvents(runCtx))
	})
}

// Continue resumes from the current transcript.
func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		return errors.New("agent is already processing")
	}
	msgs := a.state.Messages
	a.mu.Unlock()
	if len(msgs) == 0 {
		return errors.New("no messages to continue from")
	}
	last := msgs[len(msgs)-1]
	if last.Role() == "assistant" {
		if drained := a.steeringQ.drain(); len(drained) > 0 {
			return a.runWithLifecycle(ctx, func(runCtx context.Context) {
				Run(runCtx, drained, a.contextSnapshot(), a.loopConfig(true), a.processEvents(runCtx))
			})
		}
		if drained := a.followUpQ.drain(); len(drained) > 0 {
			return a.runWithLifecycle(ctx, func(runCtx context.Context) {
				Run(runCtx, drained, a.contextSnapshot(), a.loopConfig(false), a.processEvents(runCtx))
			})
		}
		return errors.New("cannot continue from message role: assistant")
	}
	return a.runWithLifecycle(ctx, func(runCtx context.Context) {
		Continue(runCtx, a.contextSnapshot(), a.loopConfig(false), a.processEvents(runCtx))
	})
}

func (a *Agent) contextSnapshot() *Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &Context{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     append([]AgentMessage{}, a.state.Messages...),
		Tools:        append([]Tool{}, a.state.Tools...),
	}
}

func (a *Agent) loopConfig(skipInitialSteeringPoll bool) *Config {
	skip := skipInitialSteeringPoll
	reasoning := a.state.ThinkingLevel
	if reasoning == llm.ThinkingOff {
		reasoning = ""
	}
	convert := a.opts.ConvertToLLM
	if convert == nil {
		convert = defaultConvertToLLM
	}
	return &Config{
		Model:            a.state.Model,
		Reasoning:        reasoning,
		Options:          a.opts.Options,
		Stream:           a.opts.Stream,
		ConvertToLLM:     convert,
		TransformContext: a.opts.TransformContext,
		APIKey:           a.opts.APIKey,
		ToolExecution:    a.opts.ToolExecution,
		BeforeToolCall:   a.opts.BeforeToolCall,
		AfterToolCall:    a.opts.AfterToolCall,
		PrepareNextTurn: func(TurnInfo) *TurnUpdate {
			if a.opts.PrepareNextTurn == nil {
				return nil
			}
			return a.opts.PrepareNextTurn(context.Background())
		},
		GetSteeringMessages: func() []AgentMessage {
			if skip {
				skip = false
				return nil
			}
			return a.steeringQ.drain()
		},
		GetFollowUpMessages: func() []AgentMessage {
			return a.followUpQ.drain()
		},
	}
}

func (a *Agent) runWithLifecycle(ctx context.Context, executor func(context.Context)) error {
	runCtx, cancel := context.WithCancel(ctx)
	run := &activeRun{cancel: cancel, done: make(chan struct{})}

	a.mu.Lock()
	if a.active != nil {
		a.mu.Unlock()
		cancel()
		return errors.New("agent is already processing")
	}
	a.active = run
	a.state.IsStreaming = true
	a.state.StreamingMessage = nil
	a.state.ErrorMessage = ""
	a.mu.Unlock()

	func() {
		defer func() {
			if r := recover(); r != nil {
				a.handleRunFailure(runCtx, r, runCtx.Err() != nil)
			}
		}()
		executor(runCtx)
	}()

	a.mu.Lock()
	a.state.IsStreaming = false
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = map[string]bool{}
	a.active = nil
	a.mu.Unlock()
	cancel()
	close(run.done)
	return nil
}

func (a *Agent) handleRunFailure(ctx context.Context, cause any, aborted bool) {
	stop := llm.StopError
	if aborted {
		stop = llm.StopAborted
	}
	model := a.state.Model
	failure := &llm.AssistantMessage{
		Content:      []llm.Content{&llm.Text{Text: ""}},
		StopReason:   stop,
		ErrorMessage: toErrorString(cause),
		Timestamp:    time.Now(),
	}
	if model != nil {
		failure.Provider = model.ResolvedProvider()
		failure.Model = model.ID
	}
	emit := a.processEvents(ctx)
	emit(MessageStart{failure})
	emit(MessageEnd{failure})
	emit(TurnEnd{Message: failure})
	emit(AgentEnd{Messages: []AgentMessage{failure}})
}

func (a *Agent) processEvents(ctx context.Context) EventSink {
	return func(event Event) {
		a.mu.Lock()
		switch e := event.(type) {
		case MessageStart:
			a.state.StreamingMessage = e.Message
		case MessageUpdate:
			a.state.StreamingMessage = e.Message
		case MessageEnd:
			a.state.StreamingMessage = nil
			a.state.Messages = append(a.state.Messages, e.Message)
		case ToolExecutionStart:
			a.state.PendingToolCalls[e.ToolCallID] = true
		case ToolExecutionEnd:
			delete(a.state.PendingToolCalls, e.ToolCallID)
		case TurnEnd:
			if am, ok := e.Message.(*llm.AssistantMessage); ok && am.ErrorMessage != "" {
				a.state.ErrorMessage = am.ErrorMessage
			}
		case AgentEnd:
			a.state.StreamingMessage = nil
		}
		listeners := make([]Listener, 0, len(a.listeners))
		for _, l := range a.listeners {
			listeners = append(listeners, l)
		}
		a.mu.Unlock()
		for _, l := range listeners {
			l(event, ctx)
		}
	}
}

func toErrorString(v any) string {
	switch e := v.(type) {
	case error:
		return e.Error()
	case string:
		return e
	default:
		return "unknown error"
	}
}
