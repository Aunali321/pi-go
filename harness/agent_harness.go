package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

// SystemPromptContext is passed to a dynamic system-prompt function.
type SystemPromptContext struct {
	Session       *session.Session
	Model         *llm.Model
	ThinkingLevel llm.ThinkingLevel
	ActiveTools   []agent.Tool
	Resources     Resources
}

// SystemPromptFunc computes a system prompt at turn start.
type SystemPromptFunc func(SystemPromptContext) (string, error)

// AgentHarnessOptions configures an AgentHarness.
type AgentHarnessOptions struct {
	Session   *session.Session
	Tools     []agent.Tool
	Resources Resources
	// Stream issues all model requests (turn streaming, compaction, branch
	// summarization). Nil uses llm.StreamSimple, which resolves API keys from
	// the environment.
	Stream           agent.StreamFunc
	SystemPrompt     string
	SystemPromptFunc SystemPromptFunc
	StreamOptions    HarnessStreamOptions
	// Retry optionally retries generated compaction and branch-summary
	// requests on transient errors.
	Retry           *llm.RetryPolicy
	Model           *llm.Model
	ThinkingLevel   llm.ThinkingLevel
	ActiveToolNames []string
	SteeringMode    agent.QueueMode
	FollowUpMode    agent.QueueMode
}

type harnessTurnState struct {
	messages      []agent.AgentMessage
	resources     Resources
	streamOptions HarnessStreamOptions
	sessionID     string
	systemPrompt  string
	model         *llm.Model
	thinkingLevel llm.ThinkingLevel
	tools         []agent.Tool
	activeTools   []agent.Tool
}

// AgentHarness orchestrates an Agent loop over a persistent Session with
// resources, compaction, branch navigation, and a rich event/hook system.
type AgentHarness struct {
	mu            sync.Mutex
	session       *session.Session
	phase         string
	runCancel     context.CancelFunc
	runDone       chan struct{}
	pendingWrites []pendingWrite

	model            *llm.Model
	thinkingLevel    llm.ThinkingLevel
	systemPrompt     string
	systemPromptFunc SystemPromptFunc
	streamOptions    HarnessStreamOptions
	stream           agent.StreamFunc
	retry            *llm.RetryPolicy
	resources        Resources

	tools           map[string]agent.Tool
	toolOrder       []string
	activeToolNames []string

	steerQueue    []agent.AgentMessage
	followUpQueue []agent.AgentMessage
	nextTurnQueue []agent.AgentMessage
	steeringMode  agent.QueueMode
	followUpMode  agent.QueueMode

	subscribers []func(event any, ctx context.Context)

	contextHooks               []func(ContextEvent) *ContextResult
	beforeProviderRequestHooks []func(BeforeProviderRequestEvent) *BeforeProviderRequestResult
	beforeProviderPayloadHooks []func(BeforeProviderPayloadEvent) *BeforeProviderPayloadResult
	toolCallHooks              []func(ToolCallEvent) *ToolCallResult
	toolResultHooks            []func(ToolResultEvent) *ToolResultPatch
	beforeAgentStartHooks      []func(BeforeAgentStartEvent) *BeforeAgentStartResult
	sessionBeforeCompactHooks  []func(SessionBeforeCompactEvent) *SessionBeforeCompactResult
	sessionBeforeTreeHooks     []func(SessionBeforeTreeEvent) *SessionBeforeTreeResult
}

// NewAgentHarness constructs a harness.
func NewAgentHarness(opts AgentHarnessOptions) (*AgentHarness, error) {
	h := &AgentHarness{
		session:          opts.Session,
		phase:            "idle",
		model:            opts.Model,
		thinkingLevel:    opts.ThinkingLevel,
		systemPrompt:     opts.SystemPrompt,
		systemPromptFunc: opts.SystemPromptFunc,
		streamOptions:    opts.StreamOptions.clone(),
		stream:           opts.Stream,
		retry:            opts.Retry,
		resources:        opts.Resources.clone(),
		tools:            map[string]agent.Tool{},
		steeringMode:     opts.SteeringMode,
		followUpMode:     opts.FollowUpMode,
	}
	if h.thinkingLevel == "" {
		h.thinkingLevel = llm.ThinkingOff
	}
	if h.steeringMode == "" {
		h.steeringMode = agent.QueueOneAtATime
	}
	if h.followUpMode == "" {
		h.followUpMode = agent.QueueOneAtATime
	}

	var toolNames []string
	for _, t := range opts.Tools {
		toolNames = append(toolNames, t.Name())
	}
	if dups := duplicateNames(toolNames); len(dups) > 0 {
		return nil, &AgentHarnessError{Code: HarnessInvalidArg, Msg: "Duplicate tool name(s): " + strings.Join(dups, ", ")}
	}
	for _, t := range opts.Tools {
		h.tools[t.Name()] = t
		h.toolOrder = append(h.toolOrder, t.Name())
	}
	if opts.ActiveToolNames != nil {
		h.activeToolNames = append([]string{}, opts.ActiveToolNames...)
	} else {
		h.activeToolNames = append([]string{}, toolNames...)
	}
	if err := h.validateToolNames(h.activeToolNames, h.tools); err != nil {
		return nil, err
	}
	return h, nil
}

func duplicateNames(names []string) []string {
	seen := map[string]bool{}
	dupSet := map[string]bool{}
	var dups []string
	for _, n := range names {
		if seen[n] && !dupSet[n] {
			dupSet[n] = true
			dups = append(dups, n)
		}
		seen[n] = true
	}
	return dups
}

func (h *AgentHarness) validateToolNames(names []string, tools map[string]agent.Tool) error {
	if dups := duplicateNames(names); len(dups) > 0 {
		return &AgentHarnessError{Code: HarnessInvalidArg, Msg: "Duplicate active tool name(s): " + strings.Join(dups, ", ")}
	}
	var missing []string
	for _, n := range names {
		if _, ok := tools[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return &AgentHarnessError{Code: HarnessInvalidArg, Msg: "Unknown tool(s): " + strings.Join(missing, ", ")}
	}
	return nil
}

// Subscribe registers a wildcard listener for all events.
func (h *AgentHarness) Subscribe(listener func(event any, ctx context.Context)) func() {
	h.mu.Lock()
	idx := len(h.subscribers)
	h.subscribers = append(h.subscribers, listener)
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.subscribers[idx] = nil
		h.mu.Unlock()
	}
}

func (h *AgentHarness) emit(event any, ctx context.Context) {
	h.mu.Lock()
	subs := append([]func(any, context.Context){}, h.subscribers...)
	h.mu.Unlock()
	for _, s := range subs {
		if s != nil {
			s(event, ctx)
		}
	}
}

// Typed hook registration.
func (h *AgentHarness) OnContext(fn func(ContextEvent) *ContextResult) {
	h.contextHooks = append(h.contextHooks, fn)
}
func (h *AgentHarness) OnBeforeProviderRequest(fn func(BeforeProviderRequestEvent) *BeforeProviderRequestResult) {
	h.beforeProviderRequestHooks = append(h.beforeProviderRequestHooks, fn)
}
func (h *AgentHarness) OnBeforeProviderPayload(fn func(BeforeProviderPayloadEvent) *BeforeProviderPayloadResult) {
	h.beforeProviderPayloadHooks = append(h.beforeProviderPayloadHooks, fn)
}
func (h *AgentHarness) OnToolCall(fn func(ToolCallEvent) *ToolCallResult) {
	h.toolCallHooks = append(h.toolCallHooks, fn)
}
func (h *AgentHarness) OnToolResult(fn func(ToolResultEvent) *ToolResultPatch) {
	h.toolResultHooks = append(h.toolResultHooks, fn)
}
func (h *AgentHarness) OnBeforeAgentStart(fn func(BeforeAgentStartEvent) *BeforeAgentStartResult) {
	h.beforeAgentStartHooks = append(h.beforeAgentStartHooks, fn)
}
func (h *AgentHarness) OnSessionBeforeCompact(fn func(SessionBeforeCompactEvent) *SessionBeforeCompactResult) {
	h.sessionBeforeCompactHooks = append(h.sessionBeforeCompactHooks, fn)
}
func (h *AgentHarness) OnSessionBeforeTree(fn func(SessionBeforeTreeEvent) *SessionBeforeTreeResult) {
	h.sessionBeforeTreeHooks = append(h.sessionBeforeTreeHooks, fn)
}

func (h *AgentHarness) createTurnState(ctx context.Context) (*harnessTurnState, error) {
	sctx, err := h.session.BuildContext()
	if err != nil {
		return nil, err
	}
	resources := h.getResources()
	meta := h.session.GetMetadata()
	tools := h.getToolsList()
	var activeTools []agent.Tool
	for _, name := range h.activeToolNames {
		if t, ok := h.tools[name]; ok {
			activeTools = append(activeTools, t)
		}
	}
	systemPrompt := "You are a helpful assistant."
	if h.systemPromptFunc != nil {
		sp, err := h.systemPromptFunc(SystemPromptContext{
			Session: h.session, Model: h.model,
			ThinkingLevel: h.thinkingLevel, ActiveTools: activeTools, Resources: resources,
		})
		if err != nil {
			return nil, err
		}
		systemPrompt = sp
	} else if h.systemPrompt != "" {
		systemPrompt = h.systemPrompt
	}
	return &harnessTurnState{
		messages:      sctx.Messages,
		resources:     resources,
		streamOptions: h.streamOptions.clone(),
		sessionID:     meta.MetaID(),
		systemPrompt:  systemPrompt,
		model:         h.model,
		thinkingLevel: h.thinkingLevel,
		tools:         tools,
		activeTools:   activeTools,
	}, nil
}

func (h *AgentHarness) createContext(ts *harnessTurnState, systemPrompt string) *agent.Context {
	if systemPrompt == "" {
		systemPrompt = ts.systemPrompt
	}
	return &agent.Context{
		SystemPrompt: systemPrompt,
		Messages:     append([]agent.AgentMessage{}, ts.messages...),
		Tools:        append([]agent.Tool{}, ts.activeTools...),
	}
}

func (h *AgentHarness) streamOrDefault() agent.StreamFunc {
	if h.stream != nil {
		return h.stream
	}
	return llm.StreamSimple
}

// retryCallbacks reports retries of generated compaction and branch-summary
// requests as harness events.
func (h *AgentHarness) retryCallbacks(operation string) *llm.RetryCallbacks {
	return &llm.RetryCallbacks{
		OnRetryScheduled: func(attempt, maxAttempts int, delay time.Duration, errorMessage string) {
			h.emit(RetryScheduledEvent{Operation: operation, Attempt: attempt, MaxAttempts: maxAttempts, Delay: delay, ErrorMessage: errorMessage}, context.Background())
		},
		OnRetryAttemptStart: func() {
			h.emit(RetryAttemptStartEvent{Operation: operation}, context.Background())
		},
		OnRetryFinished: func(success bool, attempt int, finalError string) {
			h.emit(RetryFinishedEvent{Operation: operation}, context.Background())
		},
	}
}

func (h *AgentHarness) createStreamFn(getTurnState func() *harnessTurnState) agent.StreamFunc {
	return func(ctx context.Context, model *llm.Model, reqCtx *llm.Context, opts *llm.StreamOptions) *llm.Stream {
		ts := getTurnState()
		reqOptions := h.emitBeforeProviderRequest(model, ts.sessionID, ts.streamOptions.clone())

		streamOpts := &llm.StreamOptions{
			CacheRetention: reqOptions.CacheRetention,
			Headers:        reqOptions.Headers,
			MaxRetries:     reqOptions.MaxRetries,
			Reasoning:      opts.Reasoning,
			SessionID:      ts.sessionID,
		}
		if reqOptions.MaxRetryDelayMs > 0 {
			streamOpts.MaxRetryDelay = time.Duration(reqOptions.MaxRetryDelayMs) * time.Millisecond
		}
		if reqOptions.TimeoutMs > 0 {
			streamOpts.Timeout = time.Duration(reqOptions.TimeoutMs) * time.Millisecond
		}
		streamOpts.OnPayload = func(payload map[string]any) map[string]any {
			return h.emitBeforeProviderPayload(model, payload)
		}
		streamOpts.OnResponse = func(status int, headers map[string]string) {
			h.emit(AfterProviderResponseEvent{Status: status, Headers: headers}, ctx)
		}
		return h.streamOrDefault()(ctx, model, reqCtx, streamOpts)
	}
}

func (h *AgentHarness) emitBeforeProviderRequest(model *llm.Model, sessionID string, opts HarnessStreamOptions) HarnessStreamOptions {
	current := opts.clone()
	for _, hook := range h.beforeProviderRequestHooks {
		res := hook(BeforeProviderRequestEvent{Model: model, SessionID: sessionID, StreamOptions: current.clone()})
		if res != nil && res.StreamOptions != nil {
			current = *res.StreamOptions
		}
	}
	return current
}

func (h *AgentHarness) emitBeforeProviderPayload(model *llm.Model, payload map[string]any) map[string]any {
	current := payload
	for _, hook := range h.beforeProviderPayloadHooks {
		res := hook(BeforeProviderPayloadEvent{Model: model, Payload: current})
		if res != nil {
			current = res.Payload
		}
	}
	return current
}

func (h *AgentHarness) drainQueued(queue *[]agent.AgentMessage, mode agent.QueueMode) []agent.AgentMessage {
	h.mu.Lock()
	var messages []agent.AgentMessage
	if mode == agent.QueueAll {
		messages = *queue
		*queue = nil
	} else if len(*queue) > 0 {
		messages = (*queue)[:1]
		*queue = (*queue)[1:]
	}
	h.mu.Unlock()
	if len(messages) == 0 {
		return messages
	}
	h.emitQueueUpdate()
	return messages
}

func (h *AgentHarness) emitQueueUpdate() {
	h.mu.Lock()
	ev := QueueUpdateEvent{
		Steer:    append([]agent.AgentMessage{}, h.steerQueue...),
		FollowUp: append([]agent.AgentMessage{}, h.followUpQueue...),
		NextTurn: append([]agent.AgentMessage{}, h.nextTurnQueue...),
	}
	h.mu.Unlock()
	h.emit(ev, context.Background())
}

func (h *AgentHarness) createLoopConfig(getTurnState func() *harnessTurnState, setTurnState func(*harnessTurnState)) *agent.Config {
	ts := getTurnState()
	reasoning := ts.thinkingLevel
	if reasoning == llm.ThinkingOff {
		reasoning = ""
	}
	return &agent.Config{
		Model:        ts.model,
		Reasoning:    reasoning,
		Stream:       h.createStreamFn(getTurnState),
		ConvertToLLM: message.ConvertToLLM,
		TransformContext: func(ctx context.Context, messages []agent.AgentMessage) []agent.AgentMessage {
			res := h.emitContext(ContextEvent{Messages: append([]agent.AgentMessage{}, messages...)})
			if res != nil {
				return res.Messages
			}
			return messages
		},
		BeforeToolCall: func(ctx context.Context, c agent.BeforeToolCall) agent.BeforeToolResult {
			res := h.emitToolCall(ToolCallEvent{ToolCallID: c.ToolCall.ID, ToolName: c.ToolCall.Name, Input: c.Args})
			if res != nil {
				return agent.BeforeToolResult{Block: res.Block, Reason: res.Reason}
			}
			return agent.BeforeToolResult{}
		},
		AfterToolCall: func(ctx context.Context, c agent.AfterToolCall) *agent.AfterToolResult {
			patch := h.emitToolResult(ToolResultEvent{
				ToolCallID: c.ToolCall.ID, ToolName: c.ToolCall.Name, Input: c.Args,
				Content: c.Result.Content, Details: c.Result.Details, IsError: c.IsError,
				Usage: c.Result.Usage,
			})
			if patch == nil {
				return nil
			}
			return &agent.AfterToolResult{Content: patch.Content, Details: patch.Details, IsError: patch.IsError, Usage: patch.Usage, Terminate: patch.Terminate}
		},
		PrepareNextTurn: func(agent.TurnInfo) *agent.TurnUpdate {
			h.flushPendingSessionWrites(context.Background())
			next, err := h.createTurnState(context.Background())
			if err != nil {
				return nil
			}
			setTurnState(next)
			level := next.thinkingLevel
			return &agent.TurnUpdate{Context: h.createContext(next, ""), Model: next.model, Reasoning: &level}
		},
		GetSteeringMessages: func() []agent.AgentMessage {
			return h.drainQueued(&h.steerQueue, h.steeringMode)
		},
		GetFollowUpMessages: func() []agent.AgentMessage {
			return h.drainQueued(&h.followUpQueue, h.followUpMode)
		},
	}
}

func (h *AgentHarness) emitContext(e ContextEvent) *ContextResult {
	var last *ContextResult
	for _, hook := range h.contextHooks {
		if r := hook(e); r != nil {
			last = r
		}
	}
	return last
}
func (h *AgentHarness) emitToolCall(e ToolCallEvent) *ToolCallResult {
	var last *ToolCallResult
	for _, hook := range h.toolCallHooks {
		if r := hook(e); r != nil {
			last = r
		}
	}
	return last
}
func (h *AgentHarness) emitToolResult(e ToolResultEvent) *ToolResultPatch {
	var last *ToolResultPatch
	for _, hook := range h.toolResultHooks {
		if r := hook(e); r != nil {
			last = r
		}
	}
	return last
}

func (h *AgentHarness) flushPendingSessionWrites(ctx context.Context) {
	for {
		h.mu.Lock()
		if len(h.pendingWrites) == 0 {
			h.mu.Unlock()
			return
		}
		w := h.pendingWrites[0]
		h.pendingWrites = h.pendingWrites[1:]
		h.mu.Unlock()

		switch w.kind {
		case "message":
			h.session.AppendMessage(ctx, w.message)
		case "model_change":
			h.session.AppendModelChange(ctx, w.provider, w.modelID)
		case "thinking_level_change":
			h.session.AppendThinkingLevelChange(ctx, w.thinkingLevel)
		case "active_tools_change":
			h.session.AppendActiveToolsChange(ctx, w.activeToolNames)
		case "custom":
			h.session.AppendCustomEntry(ctx, w.customType, w.data)
		case "custom_message":
			h.session.AppendCustomMessageEntry(ctx, w.customType, w.content, w.display, w.details)
		case "label":
			h.session.AppendLabel(ctx, w.targetID, w.label)
		case "session_info":
			h.session.AppendSessionName(ctx, w.name)
		case "leaf":
			h.session.GetStorage().SetLeafID(ctx, w.leafTarget)
		}
	}
}

func (h *AgentHarness) handleAgentEvent(event agent.Event, ctx context.Context) {
	switch e := event.(type) {
	case agent.MessageEnd:
		h.session.AppendMessage(ctx, e.Message)
		h.emit(event, ctx)
	case agent.TurnEnd:
		h.emit(event, ctx)
		h.mu.Lock()
		hadPending := len(h.pendingWrites) > 0
		h.mu.Unlock()
		h.flushPendingSessionWrites(ctx)
		h.emit(SavePointEvent{HadPendingMutations: hadPending}, ctx)
	case agent.AgentEnd:
		h.flushPendingSessionWrites(ctx)
		h.mu.Lock()
		h.phase = "idle"
		nextTurnCount := len(h.nextTurnQueue)
		h.mu.Unlock()
		h.emit(event, ctx)
		h.emit(SettledEvent{NextTurnCount: nextTurnCount}, ctx)
	default:
		h.emit(event, ctx)
	}
}

func createUserMessage(text string, images []*llm.Image) *llm.UserMessage {
	content := []llm.Content{&llm.Text{Text: text}}
	for _, img := range images {
		content = append(content, img)
	}
	return &llm.UserMessage{Content: content, Timestamp: time.Now()}
}

func (h *AgentHarness) executeTurn(ctx context.Context, ts *harnessTurnState, text string, images []*llm.Image) (*llm.AssistantMessage, error) {
	active := ts
	messages := []agent.AgentMessage{createUserMessage(text, images)}

	h.mu.Lock()
	if len(h.nextTurnQueue) > 0 {
		queued := h.nextTurnQueue
		h.nextTurnQueue = nil
		h.mu.Unlock()
		h.emitQueueUpdate()
		messages = append(append([]agent.AgentMessage{}, queued...), messages...)
	} else {
		h.mu.Unlock()
	}

	beforeResult := h.emitBeforeAgentStart(BeforeAgentStartEvent{
		Prompt: text, Images: images, SystemPrompt: ts.systemPrompt, Resources: ts.resources,
	})
	systemPromptOverride := ""
	if beforeResult != nil {
		if len(beforeResult.Messages) > 0 {
			messages = append(messages, beforeResult.Messages...)
		}
		systemPromptOverride = beforeResult.SystemPrompt
	}

	runCtx, cancel := context.WithCancel(ctx)
	getTurnState := func() *harnessTurnState { return active }
	setTurnState := func(next *harnessTurnState) { active = next }

	h.mu.Lock()
	h.runCancel = cancel
	h.mu.Unlock()

	var newMessages []agent.AgentMessage
	func() {
		defer func() {
			if r := recover(); r != nil {
				newMessages = h.emitRunFailure(runCtx, active.model, fmt.Sprintf("%v", r), runCtx.Err() != nil)
			}
		}()
		newMessages = agent.Run(
			runCtx,
			messages,
			h.createContext(ts, systemPromptOverride),
			h.createLoopConfig(getTurnState, setTurnState),
			func(event agent.Event) { h.handleAgentEvent(event, runCtx) },
		)
	}()

	h.flushPendingSessionWrites(ctx)
	h.mu.Lock()
	h.runCancel = nil
	h.mu.Unlock()
	cancel()

	for i := len(newMessages) - 1; i >= 0; i-- {
		if am, ok := newMessages[i].(*llm.AssistantMessage); ok {
			return am, nil
		}
	}
	return nil, &AgentHarnessError{Code: HarnessInvalidState, Msg: "AgentHarness prompt completed without an assistant message"}
}

func (h *AgentHarness) emitBeforeAgentStart(e BeforeAgentStartEvent) *BeforeAgentStartResult {
	var last *BeforeAgentStartResult
	for _, hook := range h.beforeAgentStartHooks {
		if r := hook(e); r != nil {
			last = r
		}
	}
	return last
}

func (h *AgentHarness) emitRunFailure(ctx context.Context, model *llm.Model, errMsg string, aborted bool) []agent.AgentMessage {
	stop := llm.StopError
	if aborted {
		stop = llm.StopAborted
	}
	failure := &llm.AssistantMessage{Content: []llm.Content{&llm.Text{Text: ""}}, StopReason: stop, ErrorMessage: errMsg, Timestamp: time.Now()}
	if model != nil {
		failure.Provider = model.ResolvedProvider()
		failure.Model = model.ID
	}
	h.handleAgentEvent(agent.MessageStart{Message: failure}, ctx)
	h.handleAgentEvent(agent.MessageEnd{Message: failure}, ctx)
	h.handleAgentEvent(agent.TurnEnd{Message: failure}, ctx)
	h.handleAgentEvent(agent.AgentEnd{Messages: []agent.AgentMessage{failure}}, ctx)
	return []agent.AgentMessage{failure}
}

func (h *AgentHarness) beginRun() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase != "idle" {
		return &AgentHarnessError{Code: HarnessBusy, Msg: "AgentHarness is busy"}
	}
	h.phase = "turn"
	h.runDone = make(chan struct{})
	return nil
}

func (h *AgentHarness) endRun() {
	h.mu.Lock()
	done := h.runDone
	h.runDone = nil
	h.mu.Unlock()
	if done != nil {
		close(done)
	}
}

// Prompt runs a turn from user text.
func (h *AgentHarness) Prompt(ctx context.Context, text string, images ...*llm.Image) (*llm.AssistantMessage, error) {
	if err := h.beginRun(); err != nil {
		return nil, err
	}
	defer h.endRun()
	ts, err := h.createTurnState(ctx)
	if err != nil {
		h.setPhaseIdle()
		return nil, err
	}
	msg, err := h.executeTurn(ctx, ts, text, images)
	if err != nil {
		h.setPhaseIdle()
	}
	return msg, err
}

// Skill runs a turn that invokes a named skill.
func (h *AgentHarness) Skill(ctx context.Context, name, additionalInstructions string) (*llm.AssistantMessage, error) {
	if err := h.beginRun(); err != nil {
		return nil, err
	}
	defer h.endRun()
	ts, err := h.createTurnState(ctx)
	if err != nil {
		h.setPhaseIdle()
		return nil, err
	}
	var skill *Skill
	for i := range ts.resources.Skills {
		if ts.resources.Skills[i].Name == name {
			skill = &ts.resources.Skills[i]
			break
		}
	}
	if skill == nil {
		h.setPhaseIdle()
		return nil, &AgentHarnessError{Code: HarnessInvalidArg, Msg: "Unknown skill: " + name}
	}
	msg, err := h.executeTurn(ctx, ts, FormatSkillInvocation(*skill, additionalInstructions), nil)
	if err != nil {
		h.setPhaseIdle()
	}
	return msg, err
}

// PromptFromTemplate runs a turn from a named prompt template.
func (h *AgentHarness) PromptFromTemplate(ctx context.Context, name string, args []string) (*llm.AssistantMessage, error) {
	if err := h.beginRun(); err != nil {
		return nil, err
	}
	defer h.endRun()
	ts, err := h.createTurnState(ctx)
	if err != nil {
		h.setPhaseIdle()
		return nil, err
	}
	var tpl *PromptTemplate
	for i := range ts.resources.PromptTemplates {
		if ts.resources.PromptTemplates[i].Name == name {
			tpl = &ts.resources.PromptTemplates[i]
			break
		}
	}
	if tpl == nil {
		h.setPhaseIdle()
		return nil, &AgentHarnessError{Code: HarnessInvalidArg, Msg: "Unknown prompt template: " + name}
	}
	msg, err := h.executeTurn(ctx, ts, FormatPromptTemplateInvocation(*tpl, args), nil)
	if err != nil {
		h.setPhaseIdle()
	}
	return msg, err
}

func (h *AgentHarness) setPhaseIdle() {
	h.mu.Lock()
	h.phase = "idle"
	h.mu.Unlock()
}

// Steer queues a steering message for the current run.
func (h *AgentHarness) Steer(text string, images ...*llm.Image) error {
	h.mu.Lock()
	if h.phase == "idle" {
		h.mu.Unlock()
		return &AgentHarnessError{Code: HarnessInvalidState, Msg: "Cannot steer while idle"}
	}
	h.steerQueue = append(h.steerQueue, createUserMessage(text, images))
	h.mu.Unlock()
	h.emitQueueUpdate()
	return nil
}

// FollowUp queues a follow-up message for the current run.
func (h *AgentHarness) FollowUp(text string, images ...*llm.Image) error {
	h.mu.Lock()
	if h.phase == "idle" {
		h.mu.Unlock()
		return &AgentHarnessError{Code: HarnessInvalidState, Msg: "Cannot follow up while idle"}
	}
	h.followUpQueue = append(h.followUpQueue, createUserMessage(text, images))
	h.mu.Unlock()
	h.emitQueueUpdate()
	return nil
}

// NextTurn queues a message for the next turn.
func (h *AgentHarness) NextTurn(text string, images ...*llm.Image) {
	h.mu.Lock()
	h.nextTurnQueue = append(h.nextTurnQueue, createUserMessage(text, images))
	h.mu.Unlock()
	h.emitQueueUpdate()
}

// AppendMessage appends a message to the session (queued if a run is active).
func (h *AgentHarness) AppendMessage(ctx context.Context, message agent.AgentMessage) error {
	h.mu.Lock()
	idle := h.phase == "idle"
	if !idle {
		h.pendingWrites = append(h.pendingWrites, pendingWrite{kind: "message", message: message})
	}
	h.mu.Unlock()
	if idle {
		if _, err := h.session.AppendMessage(ctx, message); err != nil {
			return err
		}
	}
	return nil
}

// GetModel returns the active model.
func (h *AgentHarness) GetModel() *llm.Model { return h.model }

// SetModel updates the model.
func (h *AgentHarness) SetModel(ctx context.Context, model *llm.Model) error {
	previous := h.model
	h.mu.Lock()
	idle := h.phase == "idle"
	if !idle {
		h.pendingWrites = append(h.pendingWrites, pendingWrite{kind: "model_change", provider: model.ResolvedProvider(), modelID: model.ID})
	}
	h.mu.Unlock()
	if idle {
		if _, err := h.session.AppendModelChange(ctx, model.ResolvedProvider(), model.ID); err != nil {
			return err
		}
	}
	h.model = model
	h.emit(ModelUpdateEvent{Model: model, PreviousModel: previous, Source: "set"}, ctx)
	return nil
}

// GetThinkingLevel returns the active thinking level.
func (h *AgentHarness) GetThinkingLevel() llm.ThinkingLevel { return h.thinkingLevel }

// SetThinkingLevel updates the thinking level.
func (h *AgentHarness) SetThinkingLevel(ctx context.Context, level llm.ThinkingLevel) error {
	previous := h.thinkingLevel
	h.mu.Lock()
	idle := h.phase == "idle"
	if !idle {
		h.pendingWrites = append(h.pendingWrites, pendingWrite{kind: "thinking_level_change", thinkingLevel: string(level)})
	}
	h.mu.Unlock()
	if idle {
		if _, err := h.session.AppendThinkingLevelChange(ctx, string(level)); err != nil {
			return err
		}
	}
	h.thinkingLevel = level
	h.emit(ThinkingLevelUpdateEvent{Level: level, PreviousLevel: previous}, ctx)
	return nil
}

func (h *AgentHarness) getToolsList() []agent.Tool {
	out := make([]agent.Tool, 0, len(h.toolOrder))
	for _, name := range h.toolOrder {
		if t, ok := h.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// GetTools returns all registered tools.
func (h *AgentHarness) GetTools() []agent.Tool { return h.getToolsList() }

// GetActiveTools returns the active tools.
func (h *AgentHarness) GetActiveTools() []agent.Tool {
	var out []agent.Tool
	for _, name := range h.activeToolNames {
		if t, ok := h.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// SetActiveTools updates which tools are active.
func (h *AgentHarness) SetActiveTools(ctx context.Context, toolNames []string) error {
	if err := h.validateToolNames(toolNames, h.tools); err != nil {
		return err
	}
	previousTools := append([]string{}, h.toolOrder...)
	previousActive := append([]string{}, h.activeToolNames...)
	h.mu.Lock()
	idle := h.phase == "idle"
	if !idle {
		h.pendingWrites = append(h.pendingWrites, pendingWrite{kind: "active_tools_change", activeToolNames: append([]string{}, toolNames...)})
	}
	h.mu.Unlock()
	if idle {
		if _, err := h.session.AppendActiveToolsChange(ctx, toolNames); err != nil {
			return err
		}
	}
	h.activeToolNames = append([]string{}, toolNames...)
	h.emit(ToolsUpdateEvent{ToolNames: previousTools, PreviousToolNames: previousTools, ActiveToolNames: h.activeToolNames, PreviousActiveToolNames: previousActive, Source: "set"}, ctx)
	return nil
}

// GetSteeringMode / SetSteeringMode.
func (h *AgentHarness) GetSteeringMode() agent.QueueMode  { return h.steeringMode }
func (h *AgentHarness) SetSteeringMode(m agent.QueueMode) { h.steeringMode = m }
func (h *AgentHarness) GetFollowUpMode() agent.QueueMode  { return h.followUpMode }
func (h *AgentHarness) SetFollowUpMode(m agent.QueueMode) { h.followUpMode = m }

func (h *AgentHarness) getResources() Resources { return h.resources.clone() }

// GetResources returns the current resources.
func (h *AgentHarness) GetResources() Resources { return h.getResources() }

// SetResources replaces resources.
func (h *AgentHarness) SetResources(resources Resources) {
	previous := h.getResources()
	h.resources = resources.clone()
	h.emit(ResourcesUpdateEvent{Resources: h.getResources(), PreviousResources: previous}, context.Background())
}

// GetStreamOptions returns the curated stream options.
func (h *AgentHarness) GetStreamOptions() HarnessStreamOptions { return h.streamOptions.clone() }

// SetStreamOptions replaces the curated stream options.
func (h *AgentHarness) SetStreamOptions(o HarnessStreamOptions) { h.streamOptions = o.clone() }

// Abort cancels the active run and clears queues.
func (h *AgentHarness) Abort(ctx context.Context) AbortResult {
	h.mu.Lock()
	clearedSteer := h.steerQueue
	clearedFollowUp := h.followUpQueue
	h.steerQueue = nil
	h.followUpQueue = nil
	cancel := h.runCancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	h.emitQueueUpdate()
	h.WaitForIdle()
	h.emit(AbortEvent{ClearedSteer: clearedSteer, ClearedFollowUp: clearedFollowUp}, ctx)
	return AbortResult{ClearedSteer: clearedSteer, ClearedFollowUp: clearedFollowUp}
}

// AbortResult reports queues cleared by an abort.
type AbortResult struct {
	ClearedSteer    []agent.AgentMessage
	ClearedFollowUp []agent.AgentMessage
}

// WaitForIdle blocks until the active run completes.
func (h *AgentHarness) WaitForIdle() {
	h.mu.Lock()
	done := h.runDone
	h.mu.Unlock()
	if done != nil {
		<-done
	}
}
