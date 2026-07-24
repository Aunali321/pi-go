package session

import (
	"context"
	"regexp"
	"strings"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/llm"
)

// ContextEntryTransform rewrites the entry list used to build model context.
type ContextEntryTransform func(entries []SessionTreeEntry) []SessionTreeEntry

// CustomEntryProjector converts a custom entry into context messages. Custom
// entries are omitted from model context by default.
type CustomEntryProjector func(entry CustomEntry, index int, entries []SessionTreeEntry) []agent.AgentMessage

// ContextBuildOptions customizes how session entries become model context.
type ContextBuildOptions struct {
	// EntryTransforms are applied after the default compaction transform.
	EntryTransforms []ContextEntryTransform
	// EntryProjectors project custom entries into context messages, keyed by
	// custom type.
	EntryProjectors map[string]CustomEntryProjector
}

func (o ContextBuildOptions) merge(other ContextBuildOptions) ContextBuildOptions {
	merged := ContextBuildOptions{
		EntryTransforms: append(append([]ContextEntryTransform{}, o.EntryTransforms...), other.EntryTransforms...),
	}
	if len(o.EntryProjectors) > 0 || len(other.EntryProjectors) > 0 {
		merged.EntryProjectors = map[string]CustomEntryProjector{}
		for k, v := range o.EntryProjectors {
			merged.EntryProjectors[k] = v
		}
		for k, v := range other.EntryProjectors {
			merged.EntryProjectors[k] = v
		}
	}
	return merged
}

func deriveSessionContextState(pathEntries []SessionTreeEntry) SessionContext {
	thinkingLevel := "off"
	var model *ModelRef
	var activeToolNames []string

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
		}
	}
	return SessionContext{ThinkingLevel: thinkingLevel, Model: model, ActiveToolNames: activeToolNames}
}

// DefaultContextEntryTransform collapses history behind the most recent
// compaction entry: the compaction itself plus either its retained tail or
// the entries from firstKeptEntryId onward.
func DefaultContextEntryTransform(pathEntries []SessionTreeEntry) []SessionTreeEntry {
	var compaction *CompactionEntry
	for _, entry := range pathEntries {
		if c, ok := entry.(CompactionEntry); ok {
			cc := c
			compaction = &cc
		}
	}
	if compaction == nil {
		return append([]SessionTreeEntry{}, pathEntries...)
	}

	entries := []SessionTreeEntry{*compaction}
	compactionIdx := -1
	for i, e := range pathEntries {
		if e.EntryType() == "compaction" && e.EntryID() == compaction.ID {
			compactionIdx = i
			break
		}
	}
	if compaction.RetainedTail != nil {
		return append(entries, pathEntries[compactionIdx+1:]...)
	}
	if compaction.FirstKeptEntryID != "" {
		foundFirstKept := false
		for i := 0; i < compactionIdx; i++ {
			entry := pathEntries[i]
			if entry.EntryID() == compaction.FirstKeptEntryID {
				foundFirstKept = true
			}
			if foundFirstKept {
				entries = append(entries, entry)
			}
		}
	}
	return append(entries, pathEntries[compactionIdx+1:]...)
}

// BuildContextEntries applies the default compaction transform followed by
// any configured transforms.
func BuildContextEntries(pathEntries []SessionTreeEntry, opts ContextBuildOptions) []SessionTreeEntry {
	entries := DefaultContextEntryTransform(pathEntries)
	for _, transform := range opts.EntryTransforms {
		entries = append([]SessionTreeEntry{}, transform(entries)...)
	}
	return entries
}

// SessionEntryToContextMessages converts one context entry into its model
// context messages.
func SessionEntryToContextMessages(entry SessionTreeEntry, index int, entries []SessionTreeEntry, opts ContextBuildOptions) []agent.AgentMessage {
	switch e := entry.(type) {
	case MessageEntry:
		return []agent.AgentMessage{e.Message}
	case CustomMessageEntry:
		return []agent.AgentMessage{message.CreateCustomMessage(e.CustomType, e.Content, e.Display, e.Details, ParseISO(e.Time))}
	case CompactionEntry:
		msgs := []agent.AgentMessage{message.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, ParseISO(e.Time))}
		return append(msgs, e.RetainedTail...)
	case BranchSummaryEntry:
		if e.Summary != "" {
			return []agent.AgentMessage{message.CreateBranchSummaryMessage(e.Summary, e.FromID, ParseISO(e.Time))}
		}
	case CustomEntry:
		if projector, ok := opts.EntryProjectors[e.CustomType]; ok {
			return projector(e, index, entries)
		}
	}
	return nil
}

func BuildSessionContext(pathEntries []SessionTreeEntry, opts ContextBuildOptions) SessionContext {
	state := deriveSessionContextState(pathEntries)
	contextEntries := BuildContextEntries(pathEntries, opts)
	var messages []agent.AgentMessage
	for i, entry := range contextEntries {
		messages = append(messages, SessionEntryToContextMessages(entry, i, contextEntries, opts)...)
	}
	state.Messages = messages
	return state
}

// Session is a high-level view over a SessionStorage.
type Session struct {
	storage      SessionStorage
	buildOptions ContextBuildOptions
}

func NewSession(storage SessionStorage) *Session { return &Session{storage: storage} }

// NewSessionWithOptions constructs a Session with default context build
// options applied to every BuildContext call.
func NewSessionWithOptions(storage SessionStorage, opts ContextBuildOptions) *Session {
	return &Session{storage: storage, buildOptions: opts}
}

func (s *Session) GetMetadata() Metadata       { return s.storage.GetMetadata() }
func (s *Session) GetStorage() SessionStorage  { return s.storage }
func (s *Session) GetLeafID() (*string, error) { return s.storage.GetLeafID() }

func (s *Session) GetEntries(cursor *EntryCursor) []SessionTreeEntry {
	return s.storage.GetEntries(cursor)
}

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
	return s.storage.GetPathToRootOrCompaction(leafID)
}

// BuildContextEntries returns the entry list model context is built from.
func (s *Session) BuildContextEntries(opts ContextBuildOptions) ([]SessionTreeEntry, error) {
	branch, err := s.GetBranch(nil)
	if err != nil {
		return nil, err
	}
	return BuildContextEntries(branch, s.buildOptions.merge(opts)), nil
}

func (s *Session) BuildContext() (SessionContext, error) {
	return s.BuildContextWithOptions(ContextBuildOptions{})
}

func (s *Session) BuildContextWithOptions(opts ContextBuildOptions) (SessionContext, error) {
	branch, err := s.GetBranch(nil)
	if err != nil {
		return SessionContext{}, err
	}
	return BuildSessionContext(branch, s.buildOptions.merge(opts)), nil
}

func (s *Session) GetLabel(id string) (string, bool) { return s.storage.GetLabel(id) }

func (s *Session) GetSessionStats() SessionStats { return s.storage.GetSessionStats() }

func (s *Session) GetSessionName() string { return s.storage.GetSessionName() }

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

// CompactionInput carries the fields of a compaction entry to append.
type CompactionInput struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	RetainedTail     []agent.AgentMessage
	Details          any
	Usage            *llm.Usage
	FromHook         bool
}

func (s *Session) AppendCompaction(ctx context.Context, in CompactionInput) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	return s.appendTyped(ctx, CompactionEntry{
		s.base(leaf), in.Summary, in.FirstKeptEntryID, in.TokensBefore, in.RetainedTail, in.Details, in.Usage, in.FromHook,
	})
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

var sessionNameNewlines = regexp.MustCompile(`[\r\n]+`)

func (s *Session) AppendSessionName(ctx context.Context, name string) (string, error) {
	leaf, err := s.leaf()
	if err != nil {
		return "", err
	}
	sanitized := strings.TrimSpace(sessionNameNewlines.ReplaceAllString(name, " "))
	return s.appendTyped(ctx, SessionInfoEntry{s.base(leaf), sanitized})
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
	entry := BranchSummaryEntry{entryBase{ID: s.storage.CreateEntryID(), Parent: entryID, Time: nowISO()}, fromID, summary.Summary, summary.Details, summary.Usage, summary.FromHook}
	id, err := s.appendTyped(ctx, entry)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

type BranchSummaryInput struct {
	Summary  string
	Details  any
	Usage    *llm.Usage
	FromHook bool
}
