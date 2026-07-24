package compaction

import (
	"context"
	"strings"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

// CompactionDetails records files touched in compacted history.
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// CompactionResult is generated compaction data ready to persist.
type CompactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	// Usage from the LLM call(s) that generated the summary.
	Usage *llm.Usage
	// RetainedTail holds recent messages retained after compaction, stored
	// directly on the compaction entry.
	RetainedTail []agent.AgentMessage
	Details      any
}

// CompactionPreparation holds prepared inputs for a compaction run.
type CompactionPreparation struct {
	FirstKeptEntryID    string
	MessagesToSummarize []agent.AgentMessage
	TurnPrefixMessages  []agent.AgentMessage
	// RetainedTail holds recent messages retained after compaction.
	RetainedTail    []agent.AgentMessage
	IsSplitTurn     bool
	TokensBefore    int
	PreviousSummary string
	FileOps         *FileOperations
	Settings        CompactionSettings
}

// SummaryRunner issues the LLM requests behind compaction and branch
// summaries. A nil Stream uses llm.StreamSimple.
type SummaryRunner struct {
	Stream        agent.StreamFunc
	Model         *llm.Model
	ThinkingLevel llm.ThinkingLevel
	Retry         *llm.RetryPolicy
	Callbacks     *llm.RetryCallbacks
}

// completeWithRetries runs one summarization request. Summaries are
// standalone requests, so routing is isolated under a fresh session id and
// cache writes that cannot be reused are avoided.
func (r SummaryRunner) completeWithRetries(ctx context.Context, reqCtx *llm.Context, opts *llm.StreamOptions) *llm.AssistantMessage {
	stream := r.Stream
	if stream == nil {
		stream = llm.StreamSimple
	}
	o := *opts
	o.CacheRetention = llm.CacheNone
	o.SessionID = session.UUIDv7()
	return llm.RetryAssistantCall(ctx, func() *llm.AssistantMessage {
		return stream(ctx, r.Model, reqCtx, &o).Result()
	}, r.Retry, r.Callbacks)
}

func combineUsage(a, b llm.Usage) llm.Usage {
	out := llm.Usage{
		Input:        a.Input + b.Input,
		Output:       a.Output + b.Output,
		CacheRead:    a.CacheRead + b.CacheRead,
		CacheWrite:   a.CacheWrite + b.CacheWrite,
		CacheWrite1h: a.CacheWrite1h + b.CacheWrite1h,
		TotalTokens:  a.TotalTokens + b.TotalTokens,
		Cost: llm.Cost{
			Input:      a.Cost.Input + b.Cost.Input,
			Output:     a.Cost.Output + b.Cost.Output,
			CacheRead:  a.Cost.CacheRead + b.Cost.CacheRead,
			CacheWrite: a.Cost.CacheWrite + b.Cost.CacheWrite,
			Total:      a.Cost.Total + b.Cost.Total,
		},
	}
	if a.Reasoning != nil || b.Reasoning != nil {
		reasoning := 0
		if a.Reasoning != nil {
			reasoning += *a.Reasoning
		}
		if b.Reasoning != nil {
			reasoning += *b.Reasoning
		}
		out.Reasoning = &reasoning
	}
	return out
}

func getMessageFromEntry(entry session.SessionTreeEntry) (agent.AgentMessage, bool) {
	switch e := entry.(type) {
	case session.MessageEntry:
		return e.Message, true
	case session.CustomMessageEntry:
		return message.CreateCustomMessage(e.CustomType, e.Content, e.Display, e.Details, session.ParseISO(e.Timestamp())), true
	case session.BranchSummaryEntry:
		return message.CreateBranchSummaryMessage(e.Summary, e.FromID, session.ParseISO(e.Timestamp())), true
	case session.CompactionEntry:
		return message.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, session.ParseISO(e.Timestamp())), true
	}
	return nil, false
}

func getMessageFromEntryForCompaction(entry session.SessionTreeEntry) (agent.AgentMessage, bool) {
	if entry.EntryType() == "compaction" {
		return nil, false
	}
	return getMessageFromEntry(entry)
}

func extractFileOperations(messages []agent.AgentMessage, entries []session.SessionTreeEntry, prevCompactionIndex int) *FileOperations {
	ops := createFileOps()
	if prevCompactionIndex >= 0 {
		if prev, ok := entries[prevCompactionIndex].(session.CompactionEntry); ok && !prev.FromHook && prev.Details != nil {
			if d, ok := prev.Details.(map[string]any); ok {
				addStrings(ops.Read, d["readFiles"])
				addStrings(ops.Edited, d["modifiedFiles"])
			}
		}
	}
	for _, m := range messages {
		extractFileOpsFromMessage(m, ops)
	}
	return ops
}

func addStrings(set map[string]bool, v any) {
	arr, ok := v.([]any)
	if !ok {
		return
	}
	for _, item := range arr {
		if s, ok := item.(string); ok {
			set[s] = true
		}
	}
}

// PrepareCompaction selects entries for compaction, or returns nil when not applicable.
func PrepareCompaction(pathEntries []session.SessionTreeEntry, settings CompactionSettings) (*CompactionPreparation, error) {
	if len(pathEntries) == 0 || pathEntries[len(pathEntries)-1].EntryType() == "compaction" {
		return nil, nil
	}

	prevCompactionIndex := -1
	for i := len(pathEntries) - 1; i >= 0; i-- {
		if pathEntries[i].EntryType() == "compaction" {
			prevCompactionIndex = i
			break
		}
	}

	var previousSummary string
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		prev := pathEntries[prevCompactionIndex].(session.CompactionEntry)
		previousSummary = prev.Summary
		idx := -1
		if prev.FirstKeptEntryID != "" {
			for i, e := range pathEntries {
				if e.EntryID() == prev.FirstKeptEntryID {
					idx = i
					break
				}
			}
		}
		if idx >= 0 {
			boundaryStart = idx
		} else {
			boundaryStart = prevCompactionIndex + 1
		}
	}
	boundaryEnd := len(pathEntries)

	tokensBefore := estimateContextTokens(session.BuildSessionContext(pathEntries, session.ContextBuildOptions{}).Messages).Tokens

	cut := findCutPoint(pathEntries, boundaryStart, boundaryEnd, settings.KeepRecentTokens)
	firstKept := pathEntries[cut.firstKeptEntryIndex]
	if firstKept.EntryID() == "" {
		return nil, &CompactionError{Code: CompactionInvalid, Msg: "First kept entry has no UUID - session may need migration"}
	}

	historyEnd := cut.firstKeptEntryIndex
	if cut.isSplitTurn {
		historyEnd = cut.turnStartIndex
	}
	var messagesToSummarize []agent.AgentMessage
	for i := boundaryStart; i < historyEnd; i++ {
		if msg, ok := getMessageFromEntryForCompaction(pathEntries[i]); ok {
			messagesToSummarize = append(messagesToSummarize, msg)
		}
	}
	var turnPrefix []agent.AgentMessage
	if cut.isSplitTurn {
		for i := cut.turnStartIndex; i < cut.firstKeptEntryIndex; i++ {
			if msg, ok := getMessageFromEntryForCompaction(pathEntries[i]); ok {
				turnPrefix = append(turnPrefix, msg)
			}
		}
	}
	var retainedTail []agent.AgentMessage
	for i := cut.firstKeptEntryIndex; i < boundaryEnd; i++ {
		if msg, ok := getMessageFromEntryForCompaction(pathEntries[i]); ok {
			retainedTail = append(retainedTail, msg)
		}
	}
	fileOps := extractFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex)
	if cut.isSplitTurn {
		for _, m := range turnPrefix {
			extractFileOpsFromMessage(m, fileOps)
		}
	}

	return &CompactionPreparation{
		FirstKeptEntryID:    firstKept.EntryID(),
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefix,
		RetainedTail:        retainedTail,
		IsSplitTurn:         cut.isSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}, nil
}

const summarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const turnPrefixSummarizationPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`

func summarizeRequest(promptText string) *llm.Context {
	return &llm.Context{
		SystemPrompt: summarizationSystemPrompt,
		Messages: []llm.Message{&llm.UserMessage{
			Content:   []llm.Content{&llm.Text{Text: promptText}},
			Timestamp: time.Now(),
		}},
	}
}

// GenerateSummary generates or updates a conversation summary for compaction,
// returning its provider usage.
func GenerateSummary(ctx context.Context, runner SummaryRunner, messages []agent.AgentMessage, reserveTokens int, customInstructions, previousSummary string) (string, llm.Usage, error) {
	maxTokens := int(0.8 * float64(reserveTokens))
	if runner.Model.MaxTokens > 0 && runner.Model.MaxTokens < maxTokens {
		maxTokens = runner.Model.MaxTokens
	}
	base := summarizationPrompt
	if previousSummary != "" {
		base = updateSummarizationPrompt
	}
	if customInstructions != "" {
		base = base + "\n\nAdditional focus: " + customInstructions
	}
	conversationText := serializeConversation(message.ConvertToLLM(messages))
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n"
	if previousSummary != "" {
		promptText += "<previous-summary>\n" + previousSummary + "\n</previous-summary>\n\n"
	}
	promptText += base

	opts := &llm.StreamOptions{MaxTokens: maxTokens}
	if runner.Model.Reasoning && runner.ThinkingLevel != "" && runner.ThinkingLevel != llm.ThinkingOff {
		opts.Reasoning = runner.ThinkingLevel
	}
	resp := runner.completeWithRetries(ctx, summarizeRequest(promptText), opts)
	if resp.StopReason == llm.StopAborted {
		return "", llm.Usage{}, &CompactionError{Code: CompactionAborted, Msg: orDefault(resp.ErrorMessage, "Summarization aborted")}
	}
	if resp.StopReason == llm.StopError {
		return "", llm.Usage{}, &CompactionError{Code: CompactionFailed, Msg: "Summarization failed: " + orDefault(resp.ErrorMessage, "Unknown error")}
	}
	return textOf(resp.Content), resp.Usage, nil
}

func generateTurnPrefixSummary(ctx context.Context, runner SummaryRunner, messages []agent.AgentMessage, reserveTokens int) (string, llm.Usage, error) {
	maxTokens := int(0.5 * float64(reserveTokens))
	if runner.Model.MaxTokens > 0 && runner.Model.MaxTokens < maxTokens {
		maxTokens = runner.Model.MaxTokens
	}
	conversationText := serializeConversation(message.ConvertToLLM(messages))
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + turnPrefixSummarizationPrompt
	opts := &llm.StreamOptions{MaxTokens: maxTokens}
	if runner.Model.Reasoning && runner.ThinkingLevel != "" && runner.ThinkingLevel != llm.ThinkingOff {
		opts.Reasoning = runner.ThinkingLevel
	}
	resp := runner.completeWithRetries(ctx, summarizeRequest(promptText), opts)
	if resp.StopReason == llm.StopAborted {
		return "", llm.Usage{}, &CompactionError{Code: CompactionAborted, Msg: orDefault(resp.ErrorMessage, "Turn prefix summarization aborted")}
	}
	if resp.StopReason == llm.StopError {
		return "", llm.Usage{}, &CompactionError{Code: CompactionFailed, Msg: "Turn prefix summarization failed: " + orDefault(resp.ErrorMessage, "Unknown error")}
	}
	return textOf(resp.Content), resp.Usage, nil
}

// Compact generates compaction summary data from prepared history.
func Compact(ctx context.Context, prep *CompactionPreparation, runner SummaryRunner, customInstructions string) (*CompactionResult, error) {
	if prep.FirstKeptEntryID == "" {
		return nil, &CompactionError{Code: CompactionInvalid, Msg: "First kept entry has no UUID - session may need migration"}
	}

	var summary string
	var summaryUsage llm.Usage
	if prep.IsSplitTurn && len(prep.TurnPrefixMessages) > 0 {
		history := "No prior history."
		var historyUsage *llm.Usage
		if len(prep.MessagesToSummarize) > 0 {
			h, usage, err := GenerateSummary(ctx, runner, prep.MessagesToSummarize, prep.Settings.ReserveTokens, customInstructions, prep.PreviousSummary)
			if err != nil {
				return nil, err
			}
			history = h
			historyUsage = &usage
		}
		prefix, prefixUsage, err := generateTurnPrefixSummary(ctx, runner, prep.TurnPrefixMessages, prep.Settings.ReserveTokens)
		if err != nil {
			return nil, err
		}
		summary = history + "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefix
		if historyUsage != nil {
			summaryUsage = combineUsage(*historyUsage, prefixUsage)
		} else {
			summaryUsage = prefixUsage
		}
	} else {
		s, usage, err := GenerateSummary(ctx, runner, prep.MessagesToSummarize, prep.Settings.ReserveTokens, customInstructions, prep.PreviousSummary)
		if err != nil {
			return nil, err
		}
		summary = s
		summaryUsage = usage
	}

	readFiles, modifiedFiles := computeFileLists(prep.FileOps)
	summary += formatFileOperations(readFiles, modifiedFiles)

	return &CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: prep.FirstKeptEntryID,
		TokensBefore:     prep.TokensBefore,
		Usage:            &summaryUsage,
		RetainedTail:     prep.RetainedTail,
		Details:          CompactionDetails{ReadFiles: readFiles, ModifiedFiles: modifiedFiles},
	}, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

var _ = strings.TrimSpace
