package compaction

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

// FileOperations accumulates file paths touched in a range of history.
type FileOperations struct {
	Read    map[string]bool
	Written map[string]bool
	Edited  map[string]bool
}

func createFileOps() *FileOperations {
	return &FileOperations{Read: map[string]bool{}, Written: map[string]bool{}, Edited: map[string]bool{}}
}

func extractFileOpsFromMessage(message agent.AgentMessage, ops *FileOperations) {
	am, ok := message.(*llm.AssistantMessage)
	if !ok {
		return
	}
	for _, block := range am.Content {
		tc, ok := block.(*llm.ToolCall)
		if !ok {
			continue
		}
		path, ok := tc.Arguments["path"].(string)
		if !ok || path == "" {
			continue
		}
		switch tc.Name {
		case "read":
			ops.Read[path] = true
		case "write":
			ops.Written[path] = true
		case "edit":
			ops.Edited[path] = true
		}
	}
}

func computeFileLists(ops *FileOperations) (readFiles, modifiedFiles []string) {
	readFiles, modifiedFiles = []string{}, []string{}
	modified := map[string]bool{}
	for f := range ops.Edited {
		modified[f] = true
	}
	for f := range ops.Written {
		modified[f] = true
	}
	for f := range ops.Read {
		if !modified[f] {
			readFiles = append(readFiles, f)
		}
	}
	for f := range modified {
		modifiedFiles = append(modifiedFiles, f)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return
}

func formatFileOperations(readFiles, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

const toolResultMaxChars = 2000

func safeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[unserializable]"
	}
	return string(b)
}

func truncateForSummary(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "\n\n[... " + itoa(len(text)-maxChars) + " more characters truncated]"
}

func itoa(n int) string { return strconv.Itoa(n) }

func textOf(content []llm.Content) string {
	var sb strings.Builder
	for _, c := range content {
		if t, ok := c.(*llm.Text); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// serializeConversation renders LLM messages as plain text for summarization.
func serializeConversation(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		switch m := msg.(type) {
		case *llm.UserMessage:
			if content := textOf(m.Content); content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case *llm.AssistantMessage:
			var textParts, thinkingParts, toolCalls []string
			for _, block := range m.Content {
				switch b := block.(type) {
				case *llm.Text:
					textParts = append(textParts, b.Text)
				case *llm.Thinking:
					thinkingParts = append(thinkingParts, b.Thinking)
				case *llm.ToolCall:
					var args []string
					for k, v := range b.Arguments {
						args = append(args, k+"="+safeJSON(v))
					}
					sort.Strings(args)
					toolCalls = append(toolCalls, b.Name+"("+strings.Join(args, ", ")+")")
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case *llm.ToolResultMessage:
			if content := textOf(m.Content); content != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(content, toolResultMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// CompactionSettings configures compaction thresholds.
type CompactionSettings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

var DefaultCompactionSettings = CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}

func calculateContextTokens(u llm.Usage) int {
	if u.TotalTokens != 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

func assistantUsage(msg agent.AgentMessage) (llm.Usage, bool) {
	am, ok := msg.(*llm.AssistantMessage)
	if !ok {
		return llm.Usage{}, false
	}
	if am.StopReason == llm.StopAborted || am.StopReason == llm.StopError {
		return llm.Usage{}, false
	}
	if calculateContextTokens(am.Usage) == 0 {
		return llm.Usage{}, false
	}
	return am.Usage, true
}

const estimatedImageChars = 4800

func estimateContentChars(content []llm.Content) int {
	chars := 0
	for _, c := range content {
		switch b := c.(type) {
		case *llm.Text:
			chars += len(b.Text)
		case *llm.Image:
			chars += estimatedImageChars
		}
	}
	return chars
}

// estimateTokens estimates token count for one message.
func estimateTokens(msg agent.AgentMessage) int {
	switch m := msg.(type) {
	case *llm.UserMessage:
		return ceilDiv(estimateContentChars(m.Content), 4)
	case *llm.AssistantMessage:
		chars := 0
		for _, block := range m.Content {
			switch b := block.(type) {
			case *llm.Text:
				chars += len(b.Text)
			case *llm.Thinking:
				chars += len(b.Thinking)
			case *llm.ToolCall:
				chars += len(b.Name) + len(safeJSON(b.Arguments))
			}
		}
		return ceilDiv(chars, 4)
	case *message.CustomMessage:
		return ceilDiv(estimateContentChars(m.Content), 4)
	case *llm.ToolResultMessage:
		return ceilDiv(estimateContentChars(m.Content), 4)
	case *message.BashExecutionMessage:
		return ceilDiv(len(m.Command)+len(m.Output), 4)
	case *message.BranchSummaryMessage:
		return ceilDiv(len(m.Summary), 4)
	case *message.CompactionSummaryMessage:
		return ceilDiv(len(m.Summary), 4)
	}
	return 0
}

func ceilDiv(a, b int) int { return int(math.Ceil(float64(a) / float64(b))) }

// ContextUsageEstimate summarizes estimated token usage for a message list.
type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex int // -1 if none
}

func estimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
	lastUsageIdx := -1
	var lastUsage llm.Usage
	for i := len(messages) - 1; i >= 0; i-- {
		if u, ok := assistantUsage(messages[i]); ok {
			lastUsage = u
			lastUsageIdx = i
			break
		}
	}
	if lastUsageIdx == -1 {
		est := 0
		for _, m := range messages {
			est += estimateTokens(m)
		}
		return ContextUsageEstimate{Tokens: est, TrailingTokens: est, LastUsageIndex: -1}
	}
	usageTokens := calculateContextTokens(lastUsage)
	trailing := 0
	for i := lastUsageIdx + 1; i < len(messages); i++ {
		trailing += estimateTokens(messages[i])
	}
	return ContextUsageEstimate{Tokens: usageTokens + trailing, UsageTokens: usageTokens, TrailingTokens: trailing, LastUsageIndex: lastUsageIdx}
}

// ShouldCompact reports whether context usage exceeds the compaction threshold.
func ShouldCompact(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

func entryMessageRole(entry session.SessionTreeEntry) (string, bool) {
	me, ok := entry.(session.MessageEntry)
	if !ok {
		return "", false
	}
	return me.Message.Role(), true
}

func findValidCutPoints(entries []session.SessionTreeEntry, start, end int) []int {
	var cuts []int
	for i := start; i < end; i++ {
		entry := entries[i]
		if role, ok := entryMessageRole(entry); ok {
			switch role {
			case "bashExecution", "custom", "branchSummary", "compactionSummary", "user", "assistant":
				cuts = append(cuts, i)
			}
		}
		if entry.EntryType() == "branch_summary" || entry.EntryType() == "custom_message" {
			cuts = append(cuts, i)
		}
	}
	return cuts
}

func findTurnStartIndex(entries []session.SessionTreeEntry, entryIndex, start int) int {
	for i := entryIndex; i >= start; i-- {
		entry := entries[i]
		if entry.EntryType() == "branch_summary" || entry.EntryType() == "custom_message" {
			return i
		}
		if role, ok := entryMessageRole(entry); ok && (role == "user" || role == "bashExecution") {
			return i
		}
	}
	return -1
}

type cutPointResult struct {
	firstKeptEntryIndex int
	turnStartIndex      int
	isSplitTurn         bool
}

func findCutPoint(entries []session.SessionTreeEntry, start, end, keepRecentTokens int) cutPointResult {
	cuts := findValidCutPoints(entries, start, end)
	if len(cuts) == 0 {
		return cutPointResult{firstKeptEntryIndex: start, turnStartIndex: -1}
	}
	accumulated := 0
	cutIndex := cuts[0]
	for i := end - 1; i >= start; i-- {
		me, ok := entries[i].(session.MessageEntry)
		if !ok {
			continue
		}
		accumulated += estimateTokens(me.Message)
		if accumulated >= keepRecentTokens {
			for _, c := range cuts {
				if c >= i {
					cutIndex = c
					break
				}
			}
			break
		}
	}
	for cutIndex > start {
		prev := entries[cutIndex-1]
		if prev.EntryType() == "compaction" || prev.EntryType() == "message" {
			break
		}
		cutIndex--
	}
	cutRole, isMsg := entryMessageRole(entries[cutIndex])
	isUser := isMsg && cutRole == "user"
	turnStart := -1
	if !isUser {
		turnStart = findTurnStartIndex(entries, cutIndex, start)
	}
	return cutPointResult{firstKeptEntryIndex: cutIndex, turnStartIndex: turnStart, isSplitTurn: !isUser && turnStart != -1}
}
