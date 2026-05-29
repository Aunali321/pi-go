package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aunali321/pi-go/harness/env"
)

type sessionHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

func invalidSession(path, msg string, cause error) *SessionError {
	return newSessionError(SessionInvalid, fmt.Sprintf("Invalid JSONL session file %s: %s", path, msg), cause)
}

func invalidEntry(path string, line int, msg string, cause error) *SessionError {
	return newSessionError(SessionInvalidEntry, fmt.Sprintf("Invalid JSONL session file %s: line %d %s", path, line, msg), cause)
}

func parseHeaderLine(line, path string) (sessionHeader, error) {
	var h sessionHeader
	if err := json.Unmarshal([]byte(line), &h); err != nil {
		return h, invalidSession(path, "first line is not a valid session header", err)
	}
	if h.Type != "session" {
		return h, invalidSession(path, "first line is not a valid session header", nil)
	}
	if h.Version != 3 {
		return h, invalidSession(path, "unsupported session version", nil)
	}
	if h.ID == "" {
		return h, invalidSession(path, "session header is missing id", nil)
	}
	if h.Timestamp == "" {
		return h, invalidSession(path, "session header is missing timestamp", nil)
	}
	if h.Cwd == "" {
		return h, invalidSession(path, "session header is missing cwd", nil)
	}
	return h, nil
}

func parseEntryLine(line, path string, lineNumber int) (SessionTreeEntry, error) {
	entry, err := decodeEntry([]byte(line))
	if err != nil {
		return nil, invalidEntry(path, lineNumber, "is not a valid session entry", err)
	}
	if entry.EntryID() == "" {
		return nil, invalidEntry(path, lineNumber, "is missing entry id", nil)
	}
	if entry.Timestamp() == "" {
		return nil, invalidEntry(path, lineNumber, "is missing timestamp", nil)
	}
	return entry, nil
}

func headerToMetadata(h sessionHeader, path string) JsonlSessionMetadata {
	return JsonlSessionMetadata{
		SessionMetadata:   SessionMetadata{ID: h.ID, CreatedAt: h.Timestamp},
		Cwd:               h.Cwd,
		Path:              path,
		ParentSessionPath: h.ParentSession,
	}
}

// LoadJsonlSessionMetadata reads only the header line of a session file.
func LoadJsonlSessionMetadata(ctx context.Context, fs env.FileSystem, path string) (JsonlSessionMetadata, error) {
	lines, ferr := fs.ReadTextLines(ctx, path, 1)
	if _, err := fileSystemResultOrThrow(lines, ferr, "Failed to read session header "+path); err != nil {
		return JsonlSessionMetadata{}, err
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return JsonlSessionMetadata{}, invalidSession(path, "missing session header", nil)
	}
	h, err := parseHeaderLine(lines[0], path)
	if err != nil {
		return JsonlSessionMetadata{}, err
	}
	return headerToMetadata(h, path), nil
}

// JsonlSessionStorage persists the session tree to a JSONL file, caching entries
// in memory.
type JsonlSessionStorage struct {
	fs       env.FileSystem
	filePath string
	metadata JsonlSessionMetadata
	entries  []SessionTreeEntry
	byID     map[string]SessionTreeEntry
	labels   map[string]string
	leafID   *string
}

func newJsonlStorage(fs env.FileSystem, path string, h sessionHeader, entries []SessionTreeEntry, leafID *string) *JsonlSessionStorage {
	s := &JsonlSessionStorage{
		fs:       fs,
		filePath: path,
		metadata: headerToMetadata(h, path),
		entries:  entries,
		byID:     map[string]SessionTreeEntry{},
		leafID:   leafID,
	}
	for _, e := range entries {
		s.byID[e.EntryID()] = e
	}
	s.labels = buildLabelsByID(entries)
	return s
}

func OpenJsonlSessionStorage(ctx context.Context, fs env.FileSystem, path string) (*JsonlSessionStorage, error) {
	content, ferr := fs.ReadTextFile(ctx, path)
	if _, err := fileSystemResultOrThrow(content, ferr, "Failed to read session "+path); err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return nil, invalidSession(path, "missing session header", nil)
	}
	h, err := parseHeaderLine(lines[0], path)
	if err != nil {
		return nil, err
	}
	var entries []SessionTreeEntry
	var leafID *string
	for i := 1; i < len(lines); i++ {
		entry, err := parseEntryLine(lines[i], path, i+1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		leafID = leafIDAfterEntry(entry)
	}
	return newJsonlStorage(fs, path, h, entries, leafID), nil
}

func CreateJsonlSessionStorage(ctx context.Context, fs env.FileSystem, path string, cwd, sessionID, parentSessionPath string) (*JsonlSessionStorage, error) {
	h := sessionHeader{Type: "session", Version: 3, ID: sessionID, Timestamp: nowISO(), Cwd: cwd, ParentSession: parentSessionPath}
	line, _ := json.Marshal(h)
	if err := fs.WriteFile(ctx, path, append(line, '\n')); err != nil {
		if _, e := fileSystemResultOrThrow[any](nil, err, "Failed to create session "+path); e != nil {
			return nil, e
		}
	}
	return newJsonlStorage(fs, path, h, nil, nil), nil
}

func (s *JsonlSessionStorage) GetMetadata() Metadata { return s.metadata }

func (s *JsonlSessionStorage) GetLeafID() (*string, error) {
	if s.leafID != nil {
		if _, ok := s.byID[*s.leafID]; !ok {
			return nil, newSessionError(SessionInvalid, "Entry "+*s.leafID+" not found", nil)
		}
	}
	return s.leafID, nil
}

func (s *JsonlSessionStorage) has(id string) bool {
	_, ok := s.byID[id]
	return ok
}

func (s *JsonlSessionStorage) SetLeafID(ctx context.Context, leafID *string) error {
	if leafID != nil {
		if _, ok := s.byID[*leafID]; !ok {
			return newSessionError(SessionNotFound, "Entry "+*leafID+" not found", nil)
		}
	}
	entry := LeafEntry{entryBase{ID: generateEntryID(s.has), Parent: s.leafID, Time: nowISO()}, leafID}
	if err := s.appendLine(ctx, entry, "Failed to append session leaf "+entry.ID); err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	s.byID[entry.ID] = entry
	s.leafID = leafID
	return nil
}

func (s *JsonlSessionStorage) CreateEntryID() string { return generateEntryID(s.has) }

func (s *JsonlSessionStorage) appendLine(ctx context.Context, entry SessionTreeEntry, errMsg string) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return newSessionError(SessionStorageErr, errMsg+": "+err.Error(), err)
	}
	if ferr := s.fs.AppendFile(ctx, s.filePath, append(line, '\n')); ferr != nil {
		if _, e := fileSystemResultOrThrow[any](nil, ferr, errMsg); e != nil {
			return e
		}
	}
	return nil
}

func (s *JsonlSessionStorage) AppendEntry(ctx context.Context, entry SessionTreeEntry) error {
	if err := s.appendLine(ctx, entry, "Failed to append session entry "+entry.EntryID()); err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	s.byID[entry.EntryID()] = entry
	updateLabelCache(s.labels, entry)
	s.leafID = leafIDAfterEntry(entry)
	return nil
}

func (s *JsonlSessionStorage) GetEntry(id string) (SessionTreeEntry, bool) {
	e, ok := s.byID[id]
	return e, ok
}

func (s *JsonlSessionStorage) FindEntries(typ string) []SessionTreeEntry {
	var out []SessionTreeEntry
	for _, e := range s.entries {
		if e.EntryType() == typ {
			out = append(out, e)
		}
	}
	return out
}

func (s *JsonlSessionStorage) GetLabel(id string) (string, bool) {
	l, ok := s.labels[id]
	return l, ok
}

func (s *JsonlSessionStorage) GetPathToRoot(leafID *string) ([]SessionTreeEntry, error) {
	return pathToRoot(s.byID, leafID)
}

func (s *JsonlSessionStorage) GetEntries() []SessionTreeEntry {
	return append([]SessionTreeEntry{}, s.entries...)
}
