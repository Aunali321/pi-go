package compaction

import (
	"context"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

// BranchSummaryResult is the output of generating a branch summary.
type BranchSummaryResult struct {
	Summary string
	// Usage from the LLM call that generated the summary, if available.
	Usage         *llm.Usage
	ReadFiles     []string
	ModifiedFiles []string
}

// CollectEntriesResult holds entries selected for branch summarization.
type CollectEntriesResult struct {
	Entries          []session.SessionTreeEntry
	CommonAncestorID *string
}

// GenerateBranchSummaryOptions configures branch summary generation.
type GenerateBranchSummaryOptions struct {
	// Stream issues the summarization request; nil uses llm.StreamSimple.
	Stream              agent.StreamFunc
	Model               *llm.Model
	CustomInstructions  string
	ReplaceInstructions bool
	// ReserveTokens are reserved for prompt and model output. Defaults to 16384.
	ReserveTokens int
	Retry         *llm.RetryPolicy
	Callbacks     *llm.RetryCallbacks
}

// CollectEntriesForBranchSummary collects entries to summarize before moving to
// a different tree entry.
func CollectEntriesForBranchSummary(sess *session.Session, oldLeafID *string, targetID string) (CollectEntriesResult, error) {
	if oldLeafID == nil {
		return CollectEntriesResult{}, nil
	}
	oldBranch, err := sess.GetBranch(oldLeafID)
	if err != nil {
		return CollectEntriesResult{}, err
	}
	oldPath := map[string]bool{}
	for _, e := range oldBranch {
		oldPath[e.EntryID()] = true
	}
	targetPath, err := sess.GetBranch(&targetID)
	if err != nil {
		return CollectEntriesResult{}, err
	}
	var commonAncestor *string
	for i := len(targetPath) - 1; i >= 0; i-- {
		if oldPath[targetPath[i].EntryID()] {
			id := targetPath[i].EntryID()
			commonAncestor = &id
			break
		}
	}

	var entries []session.SessionTreeEntry
	current := oldLeafID
	for current != nil && (commonAncestor == nil || *current != *commonAncestor) {
		entry, ok := sess.GetEntry(*current)
		if !ok {
			return CollectEntriesResult{}, &session.SessionError{Code: session.SessionInvalid, Msg: "Entry " + *current + " not found"}
		}
		entries = append(entries, entry)
		current = entry.ParentID()
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return CollectEntriesResult{Entries: entries, CommonAncestorID: commonAncestor}, nil
}

func branchMessageFromEntry(entry session.SessionTreeEntry) (agent.AgentMessage, bool) {
	switch e := entry.(type) {
	case session.MessageEntry:
		if e.Message.Role() == "toolResult" {
			return nil, false
		}
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

func prepareBranchEntries(entries []session.SessionTreeEntry, tokenBudget int) ([]agent.AgentMessage, *FileOperations) {
	var messages []agent.AgentMessage
	fileOps := createFileOps()
	for _, entry := range entries {
		if bs, ok := entry.(session.BranchSummaryEntry); ok && !bs.FromHook && bs.Details != nil {
			if d, ok := bs.Details.(map[string]any); ok {
				addStrings(fileOps.Read, d["readFiles"])
				addStrings(fileOps.Edited, d["modifiedFiles"])
			}
		}
	}
	totalTokens := 0
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		message, ok := branchMessageFromEntry(entry)
		if !ok {
			continue
		}
		extractFileOpsFromMessage(message, fileOps)
		tokens := estimateTokens(message)
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			if entry.EntryType() == "compaction" || entry.EntryType() == "branch_summary" {
				if float64(totalTokens) < float64(tokenBudget)*0.9 {
					messages = append([]agent.AgentMessage{message}, messages...)
					totalTokens += tokens
				}
			}
			break
		}
		messages = append([]agent.AgentMessage{message}, messages...)
		totalTokens += tokens
	}
	return messages, fileOps
}

const branchSummaryPreamble = "The user explored a different conversation branch before returning here.\nSummary of that exploration:\n\n"

const branchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// GenerateBranchSummary summarizes abandoned branch entries.
func GenerateBranchSummary(ctx context.Context, entries []session.SessionTreeEntry, opts GenerateBranchSummaryOptions) (*BranchSummaryResult, error) {
	reserveTokens := opts.ReserveTokens
	if reserveTokens == 0 {
		reserveTokens = 16384
	}
	contextWindow := opts.Model.ContextWindow
	if contextWindow == 0 {
		contextWindow = 128000
	}
	tokenBudget := contextWindow - reserveTokens

	messages, fileOps := prepareBranchEntries(entries, tokenBudget)
	if len(messages) == 0 {
		return &BranchSummaryResult{Summary: "No content to summarize"}, nil
	}
	conversationText := serializeConversation(message.ConvertToLLM(messages))
	var instructions string
	switch {
	case opts.ReplaceInstructions && opts.CustomInstructions != "":
		instructions = opts.CustomInstructions
	case opts.CustomInstructions != "":
		instructions = branchSummaryPrompt + "\n\nAdditional focus: " + opts.CustomInstructions
	default:
		instructions = branchSummaryPrompt
	}
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + instructions

	reqCtx := &llm.Context{
		SystemPrompt: summarizationSystemPrompt,
		Messages: []llm.Message{&llm.UserMessage{
			Content:   []llm.Content{&llm.Text{Text: promptText}},
			Timestamp: time.Now(),
		}},
	}
	runner := SummaryRunner{Stream: opts.Stream, Model: opts.Model, Retry: opts.Retry, Callbacks: opts.Callbacks}
	resp := runner.completeWithRetries(ctx, reqCtx, &llm.StreamOptions{MaxTokens: 2048})
	if resp.StopReason == llm.StopAborted {
		return nil, &BranchSummaryError{Code: BranchSummaryAborted, Msg: orDefault(resp.ErrorMessage, "Branch summary aborted")}
	}
	if resp.StopReason == llm.StopError {
		return nil, &BranchSummaryError{Code: BranchSummaryFailed, Msg: "Branch summary failed: " + orDefault(resp.ErrorMessage, "Unknown error")}
	}

	summary := branchSummaryPreamble + textOf(resp.Content)
	readFiles, modifiedFiles := computeFileLists(fileOps)
	summary += formatFileOperations(readFiles, modifiedFiles)
	if summary == "" {
		summary = "No summary generated"
	}
	return &BranchSummaryResult{Summary: summary, Usage: &resp.Usage, ReadFiles: readFiles, ModifiedFiles: modifiedFiles}, nil
}
