package session

import (
	"context"
	"regexp"
	"sort"

	"github.com/aunali321/pi-go/harness/env"
)

func getEntriesToFork(storage SessionStorage, entryID *string, position string) ([]SessionTreeEntry, error) {
	if entryID == nil {
		return storage.GetEntries(nil), nil
	}
	target, ok := storage.GetEntry(*entryID)
	if !ok {
		return nil, newSessionError(SessionInvalidForkTarget, "Entry "+*entryID+" not found", nil)
	}
	if position == "" {
		position = "before"
	}
	var effectiveLeaf *string
	if position == "at" {
		id := target.EntryID()
		effectiveLeaf = &id
	} else {
		me, ok := target.(MessageEntry)
		if !ok || me.Message.Role() != "user" {
			return nil, newSessionError(SessionInvalidForkTarget, "Entry "+*entryID+" is not a user message", nil)
		}
		effectiveLeaf = target.ParentID()
	}
	return storage.GetPathToRootOrCompaction(effectiveLeaf)
}

// ForkOptions selects where a fork branches from.
type ForkOptions struct {
	EntryID  *string
	Position string // "before" (default) | "at"
	ID       string
}

// InMemorySessionRepo holds sessions in memory.
type InMemorySessionRepo struct {
	sessions map[string]*Session
}

func NewInMemorySessionRepo() *InMemorySessionRepo {
	return &InMemorySessionRepo{sessions: map[string]*Session{}}
}

func (r *InMemorySessionRepo) Create(id string) (*Session, error) {
	if id == "" {
		id = UUIDv7()
	}
	meta := SessionMetadata{ID: id, CreatedAt: nowISO()}
	storage, err := NewInMemorySessionStorage(meta, nil)
	if err != nil {
		return nil, err
	}
	session := NewSession(storage)
	r.sessions[id] = session
	return session, nil
}

func (r *InMemorySessionRepo) Open(metadata Metadata) (*Session, error) {
	session, ok := r.sessions[metadata.MetaID()]
	if !ok {
		return nil, newSessionError(SessionNotFound, "Session not found: "+metadata.MetaID(), nil)
	}
	return session, nil
}

func (r *InMemorySessionRepo) List() []Metadata {
	out := make([]Metadata, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s.GetMetadata())
	}
	return out
}

func (r *InMemorySessionRepo) Delete(metadata Metadata) {
	delete(r.sessions, metadata.MetaID())
}

func (r *InMemorySessionRepo) Fork(ctx context.Context, source Metadata, opts ForkOptions) (*Session, error) {
	src, err := r.Open(source)
	if err != nil {
		return nil, err
	}
	forked, err := getEntriesToFork(src.GetStorage(), opts.EntryID, opts.Position)
	if err != nil {
		return nil, err
	}
	id := opts.ID
	if id == "" {
		id = UUIDv7()
	}
	meta := SessionMetadata{ID: id, CreatedAt: nowISO()}
	storage, err := NewInMemorySessionStorage(meta, forked)
	if err != nil {
		return nil, err
	}
	session := NewSession(storage)
	r.sessions[id] = session
	return session, nil
}

var cwdLeading = regexp.MustCompile(`^[/\\]`)
var cwdSeps = regexp.MustCompile(`[/\\:]`)

func encodeCwd(cwd string) string {
	s := cwdLeading.ReplaceAllString(cwd, "")
	s = cwdSeps.ReplaceAllString(s, "-")
	return "--" + s + "--"
}

// JsonlSessionRepo stores sessions as JSONL files under a root directory,
// partitioned by working directory.
type JsonlSessionRepo struct {
	fs           env.FileSystem
	rootInput    string
	resolvedRoot string
}

func NewJsonlSessionRepo(fs env.FileSystem, sessionsRoot string) *JsonlSessionRepo {
	return &JsonlSessionRepo{fs: fs, rootInput: sessionsRoot}
}

func (r *JsonlSessionRepo) root(ctx context.Context) (string, error) {
	if r.resolvedRoot == "" {
		abs, err := r.fs.AbsolutePath(ctx, r.rootInput)
		if v, e := fileSystemResultOrThrow(abs, err, "Failed to resolve sessions root "+r.rootInput); e != nil {
			return "", e
		} else {
			r.resolvedRoot = v
		}
	}
	return r.resolvedRoot, nil
}

func (r *JsonlSessionRepo) sessionDir(ctx context.Context, cwd string) (string, error) {
	root, err := r.root(ctx)
	if err != nil {
		return "", err
	}
	dir, err := r.fs.JoinPath(ctx, []string{root, encodeCwd(cwd)})
	return fileSystemResultOrThrow(dir, err, "Failed to resolve session directory for "+cwd)
}

func (r *JsonlSessionRepo) sessionFilePath(ctx context.Context, cwd, sessionID, timestamp string) (string, error) {
	dir, err := r.sessionDir(ctx, cwd)
	if err != nil {
		return "", err
	}
	name := tsToFilename(timestamp) + "_" + sessionID + ".jsonl"
	path, perr := r.fs.JoinPath(ctx, []string{dir, name})
	return fileSystemResultOrThrow(path, perr, "Failed to resolve session file path for "+sessionID)
}

var tsReplacer = regexp.MustCompile(`[:.]`)

func tsToFilename(ts string) string { return tsReplacer.ReplaceAllString(ts, "-") }

// JsonlCreateOptions configures creation of a JSONL session.
type JsonlCreateOptions struct {
	Cwd               string
	ParentSessionPath string
	ID                string
	// Metadata is arbitrary application data stored in the session header.
	Metadata map[string]any
}

func (r *JsonlSessionRepo) Create(ctx context.Context, opts JsonlCreateOptions) (*Session, error) {
	id := opts.ID
	if id == "" {
		id = UUIDv7()
	}
	createdAt := nowISO()
	dir, err := r.sessionDir(ctx, opts.Cwd)
	if err != nil {
		return nil, err
	}
	if err := r.fs.CreateDir(ctx, dir, true); err != nil {
		if _, e := fileSystemResultOrThrow[any](nil, err, "Failed to create session directory "+dir); e != nil {
			return nil, e
		}
	}
	path, err := r.sessionFilePath(ctx, opts.Cwd, id, createdAt)
	if err != nil {
		return nil, err
	}
	storage, err := CreateJsonlSessionStorage(ctx, r.fs, path, opts.Cwd, id, opts.ParentSessionPath, opts.Metadata)
	if err != nil {
		return nil, err
	}
	return NewSession(storage), nil
}

func (r *JsonlSessionRepo) Open(ctx context.Context, metadata JsonlSessionMetadata) (*Session, error) {
	exists, ferr := r.fs.Exists(ctx, metadata.Path)
	if v, e := fileSystemResultOrThrow(exists, ferr, "Failed to check session "+metadata.Path); e != nil {
		return nil, e
	} else if !v {
		return nil, newSessionError(SessionNotFound, "Session not found: "+metadata.Path, nil)
	}
	storage, err := OpenJsonlSessionStorage(ctx, r.fs, metadata.Path)
	if err != nil {
		return nil, err
	}
	return NewSession(storage), nil
}

func (r *JsonlSessionRepo) List(ctx context.Context, cwd string) ([]JsonlSessionMetadata, error) {
	var dirs []string
	if cwd != "" {
		dir, err := r.sessionDir(ctx, cwd)
		if err != nil {
			return nil, err
		}
		dirs = []string{dir}
	} else {
		d, err := r.listSessionDirs(ctx)
		if err != nil {
			return nil, err
		}
		dirs = d
	}

	var sessions []JsonlSessionMetadata
	for _, dir := range dirs {
		exists, ferr := r.fs.Exists(ctx, dir)
		if v, e := fileSystemResultOrThrow(exists, ferr, "Failed to check session directory "+dir); e != nil {
			return nil, e
		} else if !v {
			continue
		}
		files, ferr := r.fs.ListDir(ctx, dir)
		if v, e := fileSystemResultOrThrow(files, ferr, "Failed to list sessions in "+dir); e != nil {
			return nil, e
		} else {
			files = v
		}
		for _, f := range files {
			if f.Kind == env.KindDirectory || !hasSuffix(f.Name, ".jsonl") {
				continue
			}
			meta, err := LoadJsonlSessionMetadata(ctx, r.fs, f.Path)
			if err != nil {
				if se, ok := err.(*SessionError); ok && se.Code == SessionInvalid {
					continue
				}
				return nil, err
			}
			sessions = append(sessions, meta)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return ParseISO(sessions[i].CreatedAt).After(ParseISO(sessions[j].CreatedAt))
	})
	return sessions, nil
}

func (r *JsonlSessionRepo) Delete(ctx context.Context, metadata JsonlSessionMetadata) error {
	if err := r.fs.Remove(ctx, metadata.Path, false, true); err != nil {
		if _, e := fileSystemResultOrThrow[any](nil, err, "Failed to delete session "+metadata.Path); e != nil {
			return e
		}
	}
	return nil
}

func (r *JsonlSessionRepo) Fork(ctx context.Context, source JsonlSessionMetadata, opts JsonlCreateOptions, fork ForkOptions) (*Session, error) {
	src, err := r.Open(ctx, source)
	if err != nil {
		return nil, err
	}
	forked, err := getEntriesToFork(src.GetStorage(), fork.EntryID, fork.Position)
	if err != nil {
		return nil, err
	}
	id := opts.ID
	if id == "" {
		id = UUIDv7()
	}
	createdAt := nowISO()
	dir, err := r.sessionDir(ctx, opts.Cwd)
	if err != nil {
		return nil, err
	}
	if err := r.fs.CreateDir(ctx, dir, true); err != nil {
		if _, e := fileSystemResultOrThrow[any](nil, err, "Failed to create session directory "+dir); e != nil {
			return nil, e
		}
	}
	path, err := r.sessionFilePath(ctx, opts.Cwd, id, createdAt)
	if err != nil {
		return nil, err
	}
	parentPath := opts.ParentSessionPath
	if parentPath == "" {
		parentPath = source.Path
	}
	metadata := opts.Metadata
	if metadata == nil {
		metadata = source.Metadata
	}
	storage, err := CreateJsonlSessionStorage(ctx, r.fs, path, opts.Cwd, id, parentPath, metadata)
	if err != nil {
		return nil, err
	}
	for _, entry := range forked {
		if err := storage.AppendEntry(ctx, entry); err != nil {
			return nil, err
		}
	}
	return NewSession(storage), nil
}

func (r *JsonlSessionRepo) listSessionDirs(ctx context.Context) ([]string, error) {
	root, err := r.root(ctx)
	if err != nil {
		return nil, err
	}
	exists, ferr := r.fs.Exists(ctx, root)
	if v, e := fileSystemResultOrThrow(exists, ferr, "Failed to check sessions root "+root); e != nil {
		return nil, e
	} else if !v {
		return nil, nil
	}
	entries, ferr := r.fs.ListDir(ctx, root)
	if v, e := fileSystemResultOrThrow(entries, ferr, "Failed to list sessions root "+root); e != nil {
		return nil, e
	} else {
		entries = v
	}
	var dirs []string
	for _, e := range entries {
		if e.Kind == env.KindDirectory {
			dirs = append(dirs, e.Path)
		}
	}
	return dirs, nil
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
