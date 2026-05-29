package session

import (
	"context"
	"strings"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/llm"
)

func BuildSessionContext(pathEntries []SessionTreeEntry) SessionContext {
	thinkingLevel := "off"
	var model *ModelRef
	var activeToolNames []string
	var compaction *CompactionEntry

	for _, entry := range pathEntries {
		switch e := entry.(type) {
		case ThinkingLevelChangeEntry:
			thinkingLevel = e.ThinkingLevel
		case ModelChangeEntry:
			model = &ModelRef{Provider: e.Provider, ModelID: e.ModelID}
		case MessageEntry:
			if am, ok := e.Message.(*llm.AssistantMessage); ok {
				model = &ModelRef{Provider: am.Provider, ModelID: am.Model}
			}
		case ActiveToolsChangeEntry:
			activeToolNames = append([]string{}, e.ActiveToolNames...)
		case CompactionEntry:
			c := e
			compaction = &c
		}
	}

	var messages []agent.AgentMessage
	appendMessage := func(entry SessionTreeEntry) {
		switch e := entry.(type) {
		case MessageEntry:
			messages = append(messages, e.Message)
		case CustomMessageEntry:
			messages = append(messages, message.CreateCustomMessage(e.CustomType, e.Content, e.Display, e.Details, ParseISO(e.Time)))
		case BranchSummaryEntry:
			if e.Summary != "" {
				messages = append(messages, message.CreateBranchSummaryMessage(e.Summary, e.FromID, ParseISO(e.Time)))
			}
		}
	}

	if compaction != nil {
		messages = append(messages, message.CreateCompactionSummaryMessage(compaction.Summary, compaction.TokensBefore, ParseISO(compaction.Time)))
		compactionIdx := -1
		for i, e := range pathEntries {
			if e.EntryType() == "compaction" && e.EntryID() == compaction.ID {
				compactionIdx = i
				break
			}
		}
		foundFirstKept := false
		for i := 0; i < compactionIdx; i++ {
			entry := pathEntries[i]
			if entry.EntryID() == compaction.FirstKeptEntryID {
				foundFirstKept = true
			}
			if foundFirstKept {
				appendMessage(entry)
			}
		}
		for i := compactionIdx + 1; i < len(pathEntries); i++ {
			appendMessage(pathEntries[i])
		}
	} else {
		for _, entry := range pathEntries {
			appendMessage(entry)
		}
	}

	return SessionContext{Messages: messages, ThinkingLevel: thinkingLevel, Model: model, ActiveToolNames: activeToolNames}
}

// Session is a high-level view over a SessionStorage.
type Session struct {
	storage SessionStorage
}

func NewSession(storage SessionStorage) *Session { return &Session{storage} }

func (s *Session) GetMetadata() Metadata          { return s.storage.GetMetadata() }
func (s *Session) GetStorage() SessionStorage     { return s.storage }
func (s *Session) GetLeafID() (*string, error)    { return s.storage.GetLeafID() }
func (s *Session) GetEntries() []SessionTreeEntry { return s.storage.GetEntries() }

func (s *Session) GetEntry(id string) (SessionTreeEntry, bool) { return s.storage.GetEntry(id) }

func (s *Session) GetBranch(fromID *string) ([]SessionTreeEntry, error) {
	leafID := fromID
	if leafID == nil {
		l, err := s.storage.GetLeafID()
		if err != nil {
			return nil, err
		}
		leafID = l
	}
	return s.storage.GetPathToRoot(leafID)
}

func (s *Session) BuildContext() (SessionContext, error) {
	branch, err := s.GetBranch(nil)
	if err != nil {
		return SessionContext{}, err
	}
	return BuildSessionContext(branch), nil
}

func (s *Session) GetLabel(id string) (string, bool) { return s.storage.GetLabel(id) }

func (s *Session) GetSessionName() string {
	entries := s.storage.FindEntries("session_info")
	for i := len(entries) - 1; i >= 0; i-- {
		if e, ok := entries[i].(SessionInfoEntry); ok {
			if name := strings.TrimSpace(e.Name); name != "" {
				return name
			}
		}
	}
	return ""
}

func (s *Session) base(parent *string) entryBase {
	return entryBase{ID: s.storage.CreateEntryID(), Parent: parent, Time: nowISO()}
}

func (s *Session) appendTyped(ctx context.Context, entry SessionTreeEntry) (string, error) {
	if err := s.storage.AppendEntry(ctx, entry); err != nil {
		return "", err
	}
	return entry.EntryID(), nil
}

func (s *Session) leaf() (*string, error) { return s.storage.GetLeafID() }

func (s *Session) AppendMessage(ctx context.Context, message agent.AgentMessage) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, MessageEntry{s.base(leaf), message})
}

func (s *Session) AppendThinkingLevelChange(ctx context.Context, level string) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, ThinkingLevelChangeEntry{s.base(leaf), level})
}

func (s *Session) AppendModelChange(ctx context.Context, provider, modelID string) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, ModelChangeEntry{s.base(leaf), provider, modelID})
}

func (s *Session) AppendActiveToolsChange(ctx context.Context, names []string) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, ActiveToolsChangeEntry{s.base(leaf), append([]string{}, names...)})
}

func (s *Session) AppendCompaction(ctx context.Context, summary, firstKeptEntryID string, tokensBefore int, details any, fromHook bool) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, CompactionEntry{s.base(leaf), summary, firstKeptEntryID, tokensBefore, details, fromHook})
}

func (s *Session) AppendCustomEntry(ctx context.Context, customType string, data any) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, CustomEntry{s.base(leaf), customType, data})
}

func (s *Session) AppendCustomMessageEntry(ctx context.Context, customType string, content []llm.Content, display bool, details any) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, CustomMessageEntry{s.base(leaf), customType, content, details, display})
}

func (s *Session) AppendLabel(ctx context.Context, targetID string, label *string) (string, error) {
	if _, ok := s.storage.GetEntry(targetID); !ok {
		return "", newSessionError(SessionNotFound, "Entry "+targetID+" not found", nil)
	}
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, LabelEntry{s.base(leaf), targetID, label})
}

func (s *Session) AppendSessionName(ctx context.Context, name string) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, SessionInfoEntry{s.base(leaf), strings.TrimSpace(name)})
}

// MoveTo sets the active leaf, optionally recording a branch summary.
func (s *Session) MoveTo(ctx context.Context, entryID *string, summary *BranchSummaryInput) (*string, error) {
	if entryID != nil {
		if _, ok := s.storage.GetEntry(*entryID); !ok {
			return nil, newSessionError(SessionNotFound, "Entry "+*entryID+" not found", nil)
		}
	}
	if err := s.storage.SetLeafID(ctx, entryID); err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, nil
	}
	fromID := "root"
	if entryID != nil {
		fromID = *entryID
	}
	entry := BranchSummaryEntry{entryBase{ID: s.storage.CreateEntryID(), Parent: entryID, Time: nowISO()}, fromID, summary.Summary, summary.Details, summary.FromHook}
	id, err := s.appendTyped(ctx, entry)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

type BranchSummaryInput struct {
	Summary  string
	Details  any
	FromHook bool
}
