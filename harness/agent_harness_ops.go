package harness

import (
	"context"

	"github.com/aunali321/pi-go/harness/compaction"
	"github.com/aunali321/pi-go/harness/session"

	"github.com/aunali321/pi-go/llm"
)

// GetSession returns the underlying session.
func (h *AgentHarness) GetSession() *session.Session { return h.session }

func (h *AgentHarness) emitSessionBeforeCompact(e SessionBeforeCompactEvent) *SessionBeforeCompactResult {
	var last *SessionBeforeCompactResult
	for _, hook := range h.sessionBeforeCompactHooks {
		if r := hook(e); r != nil {
			last = r
		}
	}
	return last
}

func (h *AgentHarness) emitSessionBeforeTree(e SessionBeforeTreeEvent) *SessionBeforeTreeResult {
	var last *SessionBeforeTreeResult
	for _, hook := range h.sessionBeforeTreeHooks {
		if r := hook(e); r != nil {
			last = r
		}
	}
	return last
}

// Compact summarizes older history into a compaction entry.
func (h *AgentHarness) Compact(ctx context.Context, customInstructions string) (*compaction.CompactionResult, error) {
	h.mu.Lock()
	if h.phase != "idle" {
		h.mu.Unlock()
		return nil, &AgentHarnessError{Code: HarnessBusy, Msg: "compact() requires idle harness"}
	}
	h.phase = "compaction"
	h.mu.Unlock()
	defer h.setPhaseIdle()

	model := h.model
	if model == nil {
		return nil, &AgentHarnessError{Code: HarnessInvalidState, Msg: "No model set for compaction"}
	}
	if h.getAuth == nil {
		return nil, &AgentHarnessError{Code: HarnessAuth, Msg: "No auth available for compaction"}
	}
	auth, err := h.getAuth(model)
	if err != nil || auth == nil {
		return nil, &AgentHarnessError{Code: HarnessAuth, Msg: "No auth available for compaction"}
	}

	branchEntries, err := h.session.GetBranch(nil)
	if err != nil {
		return nil, err
	}
	prep, err := compaction.PrepareCompaction(branchEntries, compaction.DefaultCompactionSettings)
	if err != nil {
		return nil, err
	}
	if prep == nil {
		return nil, &AgentHarnessError{Code: HarnessCompaction, Msg: "Nothing to compact"}
	}

	hookResult := h.emitSessionBeforeCompact(SessionBeforeCompactEvent{
		Preparation: prep, BranchEntries: branchEntries, CustomInstructions: customInstructions,
	})
	if hookResult != nil && hookResult.Cancel {
		return nil, &AgentHarnessError{Code: HarnessCompaction, Msg: "Compaction cancelled"}
	}

	var result *compaction.CompactionResult
	provided := hookResult != nil && hookResult.Compaction != nil
	if provided {
		result = hookResult.Compaction
	} else {
		result, err = compaction.Compact(ctx, prep, model, auth.APIKey, auth.Headers, customInstructions, h.thinkingLevel)
		if err != nil {
			return nil, err
		}
	}

	entryID, err := h.session.AppendCompaction(ctx, result.Summary, result.FirstKeptEntryID, result.TokensBefore, result.Details, provided)
	if err != nil {
		return nil, err
	}
	if entry, ok := h.session.GetEntry(entryID); ok {
		if ce, ok := entry.(session.CompactionEntry); ok {
			h.emit(SessionCompactEvent{CompactionEntry: ce, FromHook: provided}, ctx)
		}
	}
	return result, nil
}

// NavigateTree moves the session leaf to a target entry, optionally summarizing
// the branch left behind.
func (h *AgentHarness) NavigateTree(ctx context.Context, targetID string, summarize bool, customInstructions string, replaceInstructions bool, label string) (NavigateTreeResult, error) {
	h.mu.Lock()
	if h.phase != "idle" {
		h.mu.Unlock()
		return NavigateTreeResult{}, &AgentHarnessError{Code: HarnessBusy, Msg: "navigateTree() requires idle harness"}
	}
	h.phase = "branch_summary"
	h.mu.Unlock()
	defer h.setPhaseIdle()

	oldLeafID, err := h.session.GetLeafID()
	if err != nil {
		return NavigateTreeResult{}, err
	}
	if oldLeafID != nil && *oldLeafID == targetID {
		return NavigateTreeResult{Cancelled: false}, nil
	}
	targetEntry, ok := h.session.GetEntry(targetID)
	if !ok {
		return NavigateTreeResult{}, &AgentHarnessError{Code: HarnessInvalidArg, Msg: "Entry " + targetID + " not found"}
	}

	collected, err := compaction.CollectEntriesForBranchSummary(h.session, oldLeafID, targetID)
	if err != nil {
		return NavigateTreeResult{}, err
	}

	prep := TreePreparation{
		TargetID:            targetID,
		OldLeafID:           oldLeafID,
		CommonAncestorID:    collected.CommonAncestorID,
		EntriesToSummarize:  collected.Entries,
		UserWantsSummary:    summarize,
		CustomInstructions:  customInstructions,
		ReplaceInstructions: replaceInstructions,
		Label:               label,
	}
	hookResult := h.emitSessionBeforeTree(SessionBeforeTreeEvent{Preparation: prep})
	if hookResult != nil && hookResult.Cancel {
		return NavigateTreeResult{Cancelled: true}, nil
	}

	var summaryText string
	var summaryDetails any
	fromHookSummary := false
	if hookResult != nil && hookResult.Summary != nil {
		summaryText = hookResult.Summary.Summary
		summaryDetails = hookResult.Summary.Details
		fromHookSummary = true
	}

	if summaryText == "" && summarize && len(collected.Entries) > 0 {
		model := h.model
		if model == nil {
			return NavigateTreeResult{}, &AgentHarnessError{Code: HarnessInvalidState, Msg: "No model set for branch summary"}
		}
		if h.getAuth == nil {
			return NavigateTreeResult{}, &AgentHarnessError{Code: HarnessAuth, Msg: "No auth available for branch summary"}
		}
		auth, err := h.getAuth(model)
		if err != nil || auth == nil {
			return NavigateTreeResult{}, &AgentHarnessError{Code: HarnessAuth, Msg: "No auth available for branch summary"}
		}
		ci := customInstructions
		ri := replaceInstructions
		if hookResult != nil {
			if hookResult.CustomInstructions != "" {
				ci = hookResult.CustomInstructions
			}
			if hookResult.ReplaceInstructions {
				ri = true
			}
		}
		bs, err := compaction.GenerateBranchSummary(ctx, collected.Entries, compaction.GenerateBranchSummaryOptions{
			Model: model, APIKey: auth.APIKey, Headers: auth.Headers,
			CustomInstructions: ci, ReplaceInstructions: ri,
		})
		if err != nil {
			if be, ok := err.(*compaction.BranchSummaryError); ok && be.Code == compaction.BranchSummaryAborted {
				return NavigateTreeResult{Cancelled: true}, nil
			}
			return NavigateTreeResult{}, &AgentHarnessError{Code: HarnessBranchSummary, Msg: err.Error(), Err: err}
		}
		summaryText = bs.Summary
		summaryDetails = map[string]any{"readFiles": bs.ReadFiles, "modifiedFiles": bs.ModifiedFiles}
	}

	var editorText string
	var newLeafID *string
	switch e := targetEntry.(type) {
	case session.MessageEntry:
		if um, ok := e.Message.(*llm.UserMessage); ok {
			newLeafID = e.ParentID()
			editorText = textOf(um.Content)
		} else {
			newLeafID = &targetID
		}
	case session.CustomMessageEntry:
		newLeafID = e.ParentID()
		editorText = textOf(e.Content)
	default:
		newLeafID = &targetID
	}

	var summaryInput *session.BranchSummaryInput
	if summaryText != "" {
		summaryInput = &session.BranchSummaryInput{Summary: summaryText, Details: summaryDetails, FromHook: fromHookSummary}
	}
	summaryID, err := h.session.MoveTo(ctx, newLeafID, summaryInput)
	if err != nil {
		return NavigateTreeResult{}, err
	}

	var summaryEntry *session.BranchSummaryEntry
	if summaryID != nil {
		if entry, ok := h.session.GetEntry(*summaryID); ok {
			if bse, ok := entry.(session.BranchSummaryEntry); ok {
				summaryEntry = &bse
			}
		}
	}
	newLeaf, _ := h.session.GetLeafID()
	h.emit(SessionTreeEvent{NewLeafID: newLeaf, OldLeafID: oldLeafID, SummaryEntry: summaryEntry, FromHook: fromHookSummary}, ctx)
	return NavigateTreeResult{Cancelled: false, EditorText: editorText, SummaryEntry: summaryEntry}, nil
}
