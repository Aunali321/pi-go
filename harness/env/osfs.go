package env

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OSEnv is an os-backed ExecutionEnv (filesystem + shell).
type OSEnv struct {
	cwd   string
	shell string
}

// NewOSEnv creates an OS environment rooted at cwd (defaults to the process cwd).
func NewOSEnv(cwd string) *OSEnv {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "cmd"
		} else {
			shell = "/bin/sh"
		}
	}
	return &OSEnv{cwd: cwd, shell: shell}
}

func fileErr(code FileErrorCode, msg, path string, cause error) *FileError {
	return &FileError{Code: code, Msg: msg, Path: path, Err: cause}
}

func mapOSError(err error, path string) *FileError {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fileErr(FileNotFound, err.Error(), path, err)
	case errors.Is(err, os.ErrPermission):
		return fileErr(FilePermissionDenied, err.Error(), path, err)
	default:
		return fileErr(FileUnknown, err.Error(), path, err)
	}
}

func (e *OSEnv) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(e.cwd, path)
}

func (e *OSEnv) Cwd() string { return e.cwd }

func (e *OSEnv) AbsolutePath(ctx context.Context, path string) (string, error) {
	return e.resolve(path), nil
}

func (e *OSEnv) JoinPath(ctx context.Context, parts []string) (string, error) {
	return filepath.Join(parts...), nil
}

func (e *OSEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	data, err := os.ReadFile(e.resolve(path))
	if err != nil {
		return "", mapOSError(err, path)
	}
	return string(data), nil
}

func (e *OSEnv) ReadTextLines(ctx context.Context, path string, maxLines int) ([]string, error) {
	f, err := os.Open(e.resolve(path))
	if err != nil {
		return nil, mapOSError(err, path)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if maxLines > 0 && len(lines) >= maxLines {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fileErr(FileUnknown, err.Error(), path, err)
	}
	return lines, nil
}

func (e *OSEnv) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	data, err := os.ReadFile(e.resolve(path))
	if err != nil {
		return nil, mapOSError(err, path)
	}
	return data, nil
}

func (e *OSEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	full := e.resolve(path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return mapOSError(err, path)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return mapOSError(err, path)
	}
	return nil
}

func (e *OSEnv) AppendFile(ctx context.Context, path string, content []byte) error {
	full := e.resolve(path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return mapOSError(err, path)
	}
	f, err := os.OpenFile(full, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return mapOSError(err, path)
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return mapOSError(err, path)
	}
	return nil
}

func toFileInfo(path string, info os.FileInfo) FileInfo {
	kind := KindFile
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		kind = KindSymlink
	case info.IsDir():
		kind = KindDirectory
	}
	return FileInfo{
		Name:    info.Name(),
		Path:    path,
		Kind:    kind,
		Size:    info.Size(),
		MtimeMs: info.ModTime().UnixMilli(),
	}
}

func (e *OSEnv) FileInfo(ctx context.Context, path string) (FileInfo, error) {
	full := e.resolve(path)
	info, err := os.Lstat(full)
	if err != nil {
		return FileInfo{}, mapOSError(err, path)
	}
	return toFileInfo(full, info), nil
}

func (e *OSEnv) ListDir(ctx context.Context, path string) ([]FileInfo, error) {
	full := e.resolve(path)
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, mapOSError(err, path)
	}
	out := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, toFileInfo(filepath.Join(full, entry.Name()), info))
	}
	return out, nil
}

func (e *OSEnv) CanonicalPath(ctx context.Context, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(e.resolve(path))
	if err != nil {
		return "", mapOSError(err, path)
	}
	return resolved, nil
}

func (e *OSEnv) Exists(ctx context.Context, path string) (bool, error) {
	_, err := os.Lstat(e.resolve(path))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, mapOSError(err, path)
}

func (e *OSEnv) CreateDir(ctx context.Context, path string, recursive bool) error {
	full := e.resolve(path)
	var err error
	if recursive {
		err = os.MkdirAll(full, 0o755)
	} else {
		err = os.Mkdir(full, 0o755)
	}
	if err != nil {
		return mapOSError(err, path)
	}
	return nil
}

func (e *OSEnv) Remove(ctx context.Context, path string, recursive, force bool) error {
	full := e.resolve(path)
	var err error
	if recursive {
		err = os.RemoveAll(full)
	} else {
		err = os.Remove(full)
	}
	if err != nil {
		if force && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapOSError(err, path)
	}
	return nil
}

func (e *OSEnv) CreateTempDir(ctx context.Context, prefix string) (string, error) {
	if prefix == "" {
		prefix = "tmp-"
	}
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", fileErr(FileUnknown, err.Error(), "", err)
	}
	return dir, nil
}

func (e *OSEnv) CreateTempFile(ctx context.Context, prefix, suffix string) (string, error) {
	f, err := os.CreateTemp("", prefix+"*"+suffix)
	if err != nil {
		return "", fileErr(FileUnknown, err.Error(), "", err)
	}
	name := f.Name()
	f.Close()
	return name, nil
}

func (e *OSEnv) Cleanup() {}

func (e *OSEnv) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Second)
		defer cancel()
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, e.shell, "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, e.shell, "-c", command)
	}

	cmd.Dir = e.cwd
	if opts.Cwd != "" {
		cmd.Dir = e.resolve(opts.Cwd)
	}
	if len(opts.Env) > 0 {
		env := os.Environ()
		for k, v := range opts.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = streamWriter{&stdout, opts.OnStdout}
	cmd.Stderr = streamWriter{&stderr, opts.OnStderr}

	err := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}

	if ctx.Err() == context.DeadlineExceeded {
		return res, &ExecutionError{Code: ExecTimeout, Msg: "command timed out", Err: ctx.Err()}
	}
	if ctx.Err() == context.Canceled {
		return res, &ExecutionError{Code: ExecAborted, Msg: "command aborted", Err: ctx.Err()}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, &ExecutionError{Code: ExecSpawnError, Msg: err.Error(), Err: err}
	}
	return res, nil
}

type streamWriter struct {
	buf *strings.Builder
	cb  func(string)
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if w.cb != nil {
		w.cb(string(p))
	}
	return len(p), nil
}
