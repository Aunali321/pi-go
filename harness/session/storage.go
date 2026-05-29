package session

import (
	"context"
	"strings"
)

// Metadata identifies a session.
type Metadata interface {
	MetaID() string
	MetaCreatedAt() string
}

func (m SessionMetadata) MetaID() string        { return m.ID }
func (m SessionMetadata) MetaCreatedAt() string { return m.CreatedAt }

// SessionStorage is the append-only session-tree store backing a Session.
type SessionStorage interface {
	GetMetadata() Metadata
	GetLeafID() (*string, error)
	SetLeafID(ctx context.Context, leafID *string) error
	CreateEntryID() string
	AppendEntry(ctx context.Context, entry SessionTreeEntry) error
	GetEntry(id string) (SessionTreeEntry, bool)
	FindEntries(typ string) []SessionTreeEntry
	GetLabel(id string) (string, bool)
	GetPathToRoot(leafID *string) ([]SessionTreeEntry, error)
	GetEntries() []SessionTreeEntry
}

func leafIDAfterEntry(entry SessionTreeEntry) *string {
	if leaf, ok := entry.(LeafEntry); ok {
		return leaf.TargetID
	}
	id := entry.EntryID()
	return &id
}

func updateLabelCache(labels map[string]string, entry SessionTreeEntry) {
	leaf, ok := entry.(LabelEntry)
	if !ok {
		return
	}
	if leaf.Label != nil {
		if label := strings.TrimSpace(*leaf.Label); label != "" {
			labels[leaf.TargetID] = label
			return
		}
	}
	delete(labels, leaf.TargetID)
}

func buildLabelsByID(entries []SessionTreeEntry) map[string]string {
	labels := map[string]string{}
	for _, e := range entries {
		updateLabelCache(labels, e)
	}
	return labels
}

func generateEntryID(has func(string) bool) string {
	for i := 0; i < 100; i++ {
		id := UUIDv7()[:8]
		if !has(id) {
			return id
		}
	}
	return UUIDv7()
}

func pathToRoot(byID map[string]SessionTreeEntry, leafID *string) ([]SessionTreeEntry, error) {
	if leafID == nil {
		return nil, nil
	}
	current, ok := byID[*leafID]
	if !ok {
		return nil, newSessionError(SessionNotFound, "Entry "+*leafID+" not found", nil)
	}
	var path []SessionTreeEntry
	for {
		path = append([]SessionTreeEntry{current}, path...)
		parent := current.ParentID()
		if parent == nil {
			break
		}
		next, ok := byID[*parent]
		if !ok {
			return nil, newSessionError(SessionInvalid, "Entry "+*parent+" not found", nil)
		}
		current = next
	}
	return path, nil
}

// InMemorySessionStorage keeps the session tree in memory.
type InMemorySessionStorage struct {
	metadata Metadata
	entries  []SessionTreeEntry
	byID     map[string]SessionTreeEntry
	labels   map[string]string
	leafID   *string
}

func NewInMemorySessionStorage(metadata Metadata, entries []SessionTreeEntry) (*InMemorySessionStorage, error) {
	if metadata == nil {
		metadata = SessionMetadata{ID: UUIDv7(), CreatedAt: nowISO()}
	}
	s := &InMemorySessionStorage{
		metadata: metadata,
		entries:  append([]SessionTreeEntry{}, entries...),
		byID:     map[string]SessionTreeEntry{},
	}
	for _, e := range s.entries {
		s.byID[e.EntryID()] = e
		s.leafID = leafIDAfterEntry(e)
	}
	s.labels = buildLabelsByID(s.entries)
	if s.leafID != nil {
		if _, ok := s.byID[*s.leafID]; !ok {
			return nil, newSessionError(SessionInvalid, "Entry "+*s.leafID+" not found", nil)
		}
	}
	return s, nil
}

func (s *InMemorySessionStorage) GetMetadata() Metadata { return s.metadata }

func (s *InMemorySessionStorage) GetLeafID() (*string, error) {
	if s.leafID != nil {
		if _, ok := s.byID[*s.leafID]; !ok {
			return nil, newSessionError(SessionInvalid, "Entry "+*s.leafID+" not found", nil)
		}
	}
	return s.leafID, nil
}

func (s *InMemorySessionStorage) SetLeafID(ctx context.Context, leafID *string) error {
	if leafID != nil {
		if _, ok := s.byID[*leafID]; !ok {
			return newSessionError(SessionNotFound, "Entry "+*leafID+" not found", nil)
		}
	}
	entry := LeafEntry{entryBase{ID: generateEntryID(s.has), Parent: s.leafID, Time: nowISO()}, leafID}
	s.entries = append(s.entries, entry)
	s.byID[entry.ID] = entry
	s.leafID = leafID
	return nil
}

func (s *InMemorySessionStorage) has(id string) bool {
	_, ok := s.byID[id]
	return ok
}

func (s *InMemorySessionStorage) CreateEntryID() string { return generateEntryID(s.has) }

func (s *InMemorySessionStorage) AppendEntry(ctx context.Context, entry SessionTreeEntry) error {
	s.entries = append(s.entries, entry)
	s.byID[entry.EntryID()] = entry
	updateLabelCache(s.labels, entry)
	s.leafID = leafIDAfterEntry(entry)
	return nil
}

func (s *InMemorySessionStorage) GetEntry(id string) (SessionTreeEntry, bool) {
	e, ok := s.byID[id]
	return e, ok
}

func (s *InMemorySessionStorage) FindEntries(typ string) []SessionTreeEntry {
	var out []SessionTreeEntry
	for _, e := range s.entries {
		if e.EntryType() == typ {
			out = append(out, e)
		}
	}
	return out
}

func (s *InMemorySessionStorage) GetLabel(id string) (string, bool) {
	l, ok := s.labels[id]
	return l, ok
}

func (s *InMemorySessionStorage) GetPathToRoot(leafID *string) ([]SessionTreeEntry, error) {
	return pathToRoot(s.byID, leafID)
}

func (s *InMemorySessionStorage) GetEntries() []SessionTreeEntry {
	return append([]SessionTreeEntry{}, s.entries...)
}
