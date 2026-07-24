package env

import "context"

type FileKind string

const (
	KindFile      FileKind = "file"
	KindDirectory FileKind = "directory"
	KindSymlink   FileKind = "symlink"
)

type FileInfo struct {
	Name    string
	Path    string
	Kind    FileKind
	Size    int64
	MtimeMs int64
}

type ExecOptions struct {
	Cwd string
	// Env overrides inherited defaults when InheritEnv is nil or true; with
	// InheritEnv false it is the complete environment.
	Env map[string]string
	// InheritEnv controls whether the execution environment's default
	// variables are inherited. Nil defaults to true.
	InheritEnv *bool
	Timeout    float64 // seconds; 0 = none
	OnStdout   func(string)
	OnStderr   func(string)
}

type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// FileSystem is a backend-independent filesystem. Operations return typed
// *FileError values rather than panicking.
type FileSystem interface {
	Cwd() string
	AbsolutePath(ctx context.Context, path string) (string, error)
	JoinPath(ctx context.Context, parts []string) (string, error)
	ReadTextFile(ctx context.Context, path string) (string, error)
	ReadTextLines(ctx context.Context, path string, maxLines int) ([]string, error)
	ReadBinaryFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, content []byte) error
	AppendFile(ctx context.Context, path string, content []byte) error
	FileInfo(ctx context.Context, path string) (FileInfo, error)
	ListDir(ctx context.Context, path string) ([]FileInfo, error)
	CanonicalPath(ctx context.Context, path string) (string, error)
	Exists(ctx context.Context, path string) (bool, error)
	CreateDir(ctx context.Context, path string, recursive bool) error
	Remove(ctx context.Context, path string, recursive, force bool) error
	CreateTempDir(ctx context.Context, prefix string) (string, error)
	CreateTempFile(ctx context.Context, prefix, suffix string) (string, error)
	Cleanup()
}

// Shell executes commands.
type Shell interface {
	Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error)
	Cleanup()
}

// ExecutionEnv is the filesystem and shell environment used by the harness.
type ExecutionEnv interface {
	FileSystem
	Shell
}
