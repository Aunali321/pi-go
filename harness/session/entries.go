package session

import (
	"encoding/json"
	"fmt"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/llm"
)

// SessionTreeEntry is one node in the session tree.
type SessionTreeEntry interface {
	EntryType() string
	EntryID() string
	ParentID() *string
	Timestamp() string
}

type entryBase struct {
	ID     string
	Parent *string
	Time   string
}

func (e entryBase) EntryID() string   { return e.ID }
func (e entryBase) ParentID() *string { return e.Parent }
func (e entryBase) Timestamp() string { return e.Time }

func baseMap(typ string, b entryBase) map[string]any {
	return map[string]any{"type": typ, "id": b.ID, "parentId": b.Parent, "timestamp": b.Time}
}

type MessageEntry struct {
	entryBase
	Message agent.AgentMessage
}

func (MessageEntry) EntryType() string { return "message" }
func (e MessageEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("message", e.entryBase)
	m["message"] = e.Message
	return json.Marshal(m)
}

type ThinkingLevelChangeEntry struct {
	entryBase
	ThinkingLevel string
}

func (ThinkingLevelChangeEntry) EntryType() string { return "thinking_level_change" }
func (e ThinkingLevelChangeEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("thinking_level_change", e.entryBase)
	m["thinkingLevel"] = e.ThinkingLevel
	return json.Marshal(m)
}

type ModelChangeEntry struct {
	entryBase
	Provider string
	ModelID  string
}

func (ModelChangeEntry) EntryType() string { return "model_change" }
func (e ModelChangeEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("model_change", e.entryBase)
	m["provider"] = e.Provider
	m["modelId"] = e.ModelID
	return json.Marshal(m)
}

type ActiveToolsChangeEntry struct {
	entryBase
	ActiveToolNames []string
}

func (ActiveToolsChangeEntry) EntryType() string { return "active_tools_change" }
func (e ActiveToolsChangeEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("active_tools_change", e.entryBase)
	m["activeToolNames"] = e.ActiveToolNames
	return json.Marshal(m)
}

type CompactionEntry struct {
	entryBase
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          any
	FromHook         bool
}

func (CompactionEntry) EntryType() string { return "compaction" }
func (e CompactionEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("compaction", e.entryBase)
	m["summary"] = e.Summary
	m["firstKeptEntryId"] = e.FirstKeptEntryID
	m["tokensBefore"] = e.TokensBefore
	if e.Details != nil {
		m["details"] = e.Details
	}
	if e.FromHook {
		m["fromHook"] = true
	}
	return json.Marshal(m)
}

type BranchSummaryEntry struct {
	entryBase
	FromID   string
	Summary  string
	Details  any
	FromHook bool
}

func (BranchSummaryEntry) EntryType() string { return "branch_summary" }
func (e BranchSummaryEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("branch_summary", e.entryBase)
	m["fromId"] = e.FromID
	m["summary"] = e.Summary
	if e.Details != nil {
		m["details"] = e.Details
	}
	if e.FromHook {
		m["fromHook"] = true
	}
	return json.Marshal(m)
}

type CustomEntry struct {
	entryBase
	CustomType string
	Data       any
}

func (CustomEntry) EntryType() string { return "custom" }
func (e CustomEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("custom", e.entryBase)
	m["customType"] = e.CustomType
	if e.Data != nil {
		m["data"] = e.Data
	}
	return json.Marshal(m)
}

type CustomMessageEntry struct {
	entryBase
	CustomType string
	Content    []llm.Content
	Details    any
	Display    bool
}

func (CustomMessageEntry) EntryType() string { return "custom_message" }
func (e CustomMessageEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("custom_message", e.entryBase)
	m["customType"] = e.CustomType
	if e.Content == nil {
		m["content"] = []llm.Content{}
	} else {
		m["content"] = e.Content
	}
	m["display"] = e.Display
	if e.Details != nil {
		m["details"] = e.Details
	}
	return json.Marshal(m)
}

type LabelEntry struct {
	entryBase
	TargetID string
	Label    *string
}

func (LabelEntry) EntryType() string { return "label" }
func (e LabelEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("label", e.entryBase)
	m["targetId"] = e.TargetID
	m["label"] = e.Label
	return json.Marshal(m)
}

type SessionInfoEntry struct {
	entryBase
	Name string
}

func (SessionInfoEntry) EntryType() string { return "session_info" }
func (e SessionInfoEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("session_info", e.entryBase)
	if e.Name != "" {
		m["name"] = e.Name
	}
	return json.Marshal(m)
}

type LeafEntry struct {
	entryBase
	TargetID *string
}

func (LeafEntry) EntryType() string { return "leaf" }
func (e LeafEntry) MarshalJSON() ([]byte, error) {
	m := baseMap("leaf", e.entryBase)
	m["targetId"] = e.TargetID
	return json.Marshal(m)
}

func decodeEntry(data []byte) (SessionTreeEntry, error) {
	var head struct {
		Type      string  `json:"type"`
		ID        string  `json:"id"`
		ParentID  *string `json:"parentId"`
		Timestamp string  `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	base := entryBase{ID: head.ID, Parent: head.ParentID, Time: head.Timestamp}

	switch head.Type {
	case "message":
		var v struct {
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		msg, err := message.DecodeAgentMessage(v.Message)
		if err != nil {
			return nil, err
		}
		return MessageEntry{base, msg}, nil
	case "thinking_level_change":
		var v struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		json.Unmarshal(data, &v)
		return ThinkingLevelChangeEntry{base, v.ThinkingLevel}, nil
	case "model_change":
		var v struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		json.Unmarshal(data, &v)
		return ModelChangeEntry{base, v.Provider, v.ModelID}, nil
	case "active_tools_change":
		var v struct {
			ActiveToolNames []string `json:"activeToolNames"`
		}
		json.Unmarshal(data, &v)
		return ActiveToolsChangeEntry{base, v.ActiveToolNames}, nil
	case "compaction":
		var v struct {
			Summary          string          `json:"summary"`
			FirstKeptEntryID string          `json:"firstKeptEntryId"`
			TokensBefore     int             `json:"tokensBefore"`
			Details          json.RawMessage `json:"details"`
			FromHook         bool            `json:"fromHook"`
		}
		json.Unmarshal(data, &v)
		return CompactionEntry{base, v.Summary, v.FirstKeptEntryID, v.TokensBefore, rawAny(v.Details), v.FromHook}, nil
	case "branch_summary":
		var v struct {
			FromID   string          `json:"fromId"`
			Summary  string          `json:"summary"`
			Details  json.RawMessage `json:"details"`
			FromHook bool            `json:"fromHook"`
		}
		json.Unmarshal(data, &v)
		return BranchSummaryEntry{base, v.FromID, v.Summary, rawAny(v.Details), v.FromHook}, nil
	case "custom":
		var v struct {
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		json.Unmarshal(data, &v)
		return CustomEntry{base, v.CustomType, rawAny(v.Data)}, nil
	case "custom_message":
		var v struct {
			CustomType string          `json:"customType"`
			Content    json.RawMessage `json:"content"`
			Details    json.RawMessage `json:"details"`
			Display    bool            `json:"display"`
		}
		json.Unmarshal(data, &v)
		content, err := message.DecodeCustomMessageContent(v.Content)
		if err != nil {
			return nil, err
		}
		return CustomMessageEntry{base, v.CustomType, content, rawAny(v.Details), v.Display}, nil
	case "label":
		var v struct {
			TargetID string  `json:"targetId"`
			Label    *string `json:"label"`
		}
		json.Unmarshal(data, &v)
		return LabelEntry{base, v.TargetID, v.Label}, nil
	case "session_info":
		var v struct {
			Name string `json:"name"`
		}
		json.Unmarshal(data, &v)
		return SessionInfoEntry{base, v.Name}, nil
	case "leaf":
		var v struct {
			TargetID *string `json:"targetId"`
		}
		json.Unmarshal(data, &v)
		return LeafEntry{base, v.TargetID}, nil
	default:
		return nil, fmt.Errorf("unknown session entry type %q", head.Type)
	}
}

func rawAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// SessionContext is the reconstructed conversation state from a branch.
type SessionContext struct {
	Messages        []agent.AgentMessage
	ThinkingLevel   string
	Model           *ModelRef
	ActiveToolNames []string
}

type ModelRef struct {
	Provider string
	ModelID  string
}

type SessionMetadata struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
}

type JsonlSessionMetadata struct {
	SessionMetadata
	Cwd               string
	Path              string
	ParentSessionPath string
}
