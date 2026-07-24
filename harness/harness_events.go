package harness

import (
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/compaction"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

// Resources are skills and prompt templates available to the harness.
type Resources struct {
	Skills          []Skill
	PromptTemplates []PromptTemplate
}

func (r Resources) clone() Resources {
	return Resources{
		Skills:          append([]Skill{}, r.Skills...),
		PromptTemplates: append([]PromptTemplate{}, r.PromptTemplates...),
	}
}

// HarnessStreamOptions are curated provider request options.
type HarnessStreamOptions struct {
	Transport       string
	TimeoutMs       int
	MaxRetries      int
	MaxRetryDelayMs int
	Headers         map[string]string
	Metadata        map[string]any
	CacheRetention  llm.CacheRetention
}

func (o HarnessStreamOptions) clone() HarnessStreamOptions {
	c := o
	if o.Headers != nil {
		c.Headers = map[string]string{}
		for k, v := range o.Headers {
			c.Headers[k] = v
		}
	}
	if o.Metadata != nil {
		c.Metadata = map[string]any{}
		for k, v := range o.Metadata {
			c.Metadata[k] = v
		}
	}
	return c
}

// Harness own events.

type QueueUpdateEvent struct {
	Steer    []agent.AgentMessage
	FollowUp []agent.AgentMessage
	NextTurn []agent.AgentMessage
}
type SavePointEvent struct{ HadPendingMutations bool }
type AbortEvent struct {
	ClearedSteer    []agent.AgentMessage
	ClearedFollowUp []agent.AgentMessage
}
type SettledEvent struct{ NextTurnCount int }
type AfterProviderResponseEvent struct {
	Status  int
	Headers map[string]string
}
type SessionCompactEvent struct {
	CompactionEntry session.CompactionEntry
	FromHook        bool
}
type SessionTreeEvent struct {
	NewLeafID    *string
	OldLeafID    *string
	SummaryEntry *session.BranchSummaryEntry
	FromHook     bool
}
type ModelUpdateEvent struct {
	Model         *llm.Model
	PreviousModel *llm.Model
	Source        string
}
type ThinkingLevelUpdateEvent struct {
	Level         llm.ThinkingLevel
	PreviousLevel llm.ThinkingLevel
}
type ToolsUpdateEvent struct {
	ToolNames               []string
	PreviousToolNames       []string
	ActiveToolNames         []string
	PreviousActiveToolNames []string
	Source                  string
}
type ResourcesUpdateEvent struct {
	Resources         Resources
	PreviousResources Resources
}

// Hook events (carry a result).

type BeforeAgentStartEvent struct {
	Prompt       string
	Images       []*llm.Image
	SystemPrompt string
	Resources    Resources
}
type BeforeAgentStartResult struct {
	Messages     []agent.AgentMessage
	SystemPrompt string
}

type ContextEvent struct{ Messages []agent.AgentMessage }
type ContextResult struct{ Messages []agent.AgentMessage }

type BeforeProviderRequestEvent struct {
	Model         *llm.Model
	SessionID     string
	StreamOptions HarnessStreamOptions
}
type BeforeProviderRequestResult struct{ StreamOptions *HarnessStreamOptions }

type BeforeProviderPayloadEvent struct {
	Model   *llm.Model
	Payload map[string]any
}
type BeforeProviderPayloadResult struct{ Payload map[string]any }

type ToolCallEvent struct {
	ToolCallID string
	ToolName   string
	Input      map[string]any
}
type ToolCallResult struct {
	Block  bool
	Reason string
}

type ToolResultEvent struct {
	ToolCallID string
	ToolName   string
	Input      map[string]any
	Content    []llm.Content
	Details    any
	IsError    bool
	// Usage from the tool execution itself, if available.
	Usage *llm.Usage
}
type ToolResultPatch struct {
	Content   []llm.Content
	Details   any
	IsError   *bool
	Usage     *llm.Usage
	Terminate *bool
}

// Retry events report retries of generated compaction and branch-summary
// requests. Operation is "compaction" or "branch_summary".

type RetryScheduledEvent struct {
	Operation    string
	Attempt      int
	MaxAttempts  int
	Delay        time.Duration
	ErrorMessage string
}
type RetryAttemptStartEvent struct{ Operation string }
type RetryFinishedEvent struct{ Operation string }

type SessionBeforeCompactEvent struct {
	Preparation        *compaction.CompactionPreparation
	BranchEntries      []session.SessionTreeEntry
	CustomInstructions string
}
type SessionBeforeCompactResult struct {
	Cancel     bool
	Compaction *compaction.CompactionResult
}

type SessionBeforeTreeEvent struct {
	Preparation TreePreparation
}
type SessionBeforeTreeResult struct {
	Cancel              bool
	Summary             *TreeSummary
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
}
type TreeSummary struct {
	Summary string
	Details any
	// Usage from the LLM call that generated this summary, if available.
	Usage *llm.Usage
}

type TreePreparation struct {
	TargetID            string
	OldLeafID           *string
	CommonAncestorID    *string
	EntriesToSummarize  []session.SessionTreeEntry
	UserWantsSummary    bool
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
}

// NavigateTreeResult is returned by NavigateTree.
type NavigateTreeResult struct {
	Cancelled    bool
	EditorText   string
	SummaryEntry *session.BranchSummaryEntry
}

type pendingWrite struct {
	kind            string
	message         agent.AgentMessage
	provider        string
	modelID         string
	thinkingLevel   string
	activeToolNames []string
	customType      string
	data            any
	content         []llm.Content
	display         bool
	details         any
	targetID        string
	label           *string
	name            string
	leafTarget      *string
}
