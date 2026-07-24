package env

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OSEnv is an os-backed ExecutionEnv (filesystem + shell).
type OSEnv struct {
	cwd       string
	shellPath string
	shellEnv  map[string]string

	mu             sync.Mutex
	activeChildren map[*exec.Cmd]bool
}

// NewOSEnv creates an OS environment rooted at cwd (defaults to the process cwd).
func NewOSEnv(cwd string) *OSEnv {
	return NewOSEnvWith(OSEnvOptions{Cwd: cwd})
}

// OSEnvOptions configures an OSEnv.
type OSEnvOptions struct {
	Cwd string
	// ShellPath overrides bash discovery with an explicit shell binary.
	ShellPath string
	// ShellEnv holds default environment variables applied to every command.
	ShellEnv map[string]string
}

func NewOSEnvWith(opts OSEnvOptions) *OSEnv {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &OSEnv{cwd: cwd, shellPath: opts.ShellPath, shellEnv: opts.ShellEnv, activeChildren: map[*exec.Cmd]bool{}}
}

type shellConfig struct {
	shell string
	args  []string
}

func findBashOnPath() string {
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return ""
}

// shellConfigFor mirrors pi's bash discovery: an explicit shell path wins,
// then /bin/bash, then bash on PATH, then plain sh. On Windows, Git Bash
// installs are searched before PATH.
func (e *OSEnv) shellConfigFor() (shellConfig, error) {
	if e.shellPath != "" {
		if _, err := os.Stat(e.shellPath); err != nil {
			return shellConfig{}, &ExecutionError{Code: ExecShellUnavailable, Msg: "Custom shell path not found: " + e.shellPath}
		}
		return shellConfig{shell: e.shellPath, args: []string{"-c"}}, nil
	}

	if runtime.GOOS == "windows" {
		var candidates []string
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			candidates = append(candidates, pf+`\Git\bin\bash.exe`)
		}
		if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
			candidates = append(candidates, pf+`\Git\bin\bash.exe`)
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return shellConfig{shell: candidate, args: []string{"-c"}}, nil
			}
		}
		if bash := findBashOnPath(); bash != "" {
			return shellConfig{shell: bash, args: []string{"-c"}}, nil
		}
		msg := "No bash shell found. Options:\n" +
			"  1. Install Git for Windows: https://git-scm.com/download/win\n" +
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n" +
			"  3. Configure an explicit shellPath\n\nSearched Git Bash in:\n"
		for _, candidate := range candidates {
			msg += "  " + candidate + "\n"
		}
		return shellConfig{}, &ExecutionError{Code: ExecShellUnavailable, Msg: strings.TrimRight(msg, "\n")}
	}

	if _, err := os.Stat("/bin/bash"); err == nil {
		return shellConfig{shell: "/bin/bash", args: []string{"-c"}}, nil
	}
	if bash := findBashOnPath(); bash != "" {
		return shellConfig{shell: bash, args: []string{"-c"}}, nil
	}
	return shellConfig{shell: "sh", args: []string{"-c"}}, nil
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
	normalized := path
	if normalized == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			normalized = home
		}
	} else if strings.HasPrefix(normalized, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(normalized, `~\`)) {
		if home, err := os.UserHomeDir(); err == nil {
			normalized = filepath.Join(home, normalized[2:])
		}
	} else if strings.HasPrefix(normalized, "file://") {
		if u, err := url.Parse(normalized); err == nil && u.Path != "" {
			normalized = u.Path
		}
		// Malformed URLs stay ordinary paths so filesystem methods preserve
		// their non-throwing contract.
	}
	if filepath.IsAbs(normalized) {
		return filepath.Clean(normalized)
	}
	return filepath.Join(e.cwd, normalized)
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

// Cleanup terminates any commands still running.
func (e *OSEnv) Cleanup() {
	e.mu.Lock()
	children := make([]*exec.Cmd, 0, len(e.activeChildren))
	for cmd := range e.activeChildren {
		children = append(children, cmd)
	}
	e.activeChildren = map[*exec.Cmd]bool{}
	e.mu.Unlock()
	for _, cmd := range children {
		killProcessTree(cmd)
	}
}

func (e *OSEnv) shellEnvFor(extra map[string]string, inherit bool) []string {
	if !inherit {
		env := make([]string, 0, len(extra))
		for k, v := range extra {
			env = append(env, k+"="+v)
		}
		return env
	}
	env := os.Environ()
	for k, v := range e.shellEnv {
		env = append(env, k+"="+v)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func (e *OSEnv) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	res := ExecResult{}
	if ctx.Err() != nil {
		return res, &ExecutionError{Code: ExecAborted, Msg: "aborted", Err: ctx.Err()}
	}
	if opts.Timeout < 0 || math.IsInf(opts.Timeout, 0) || math.IsNaN(opts.Timeout) {
		return res, &ExecutionError{Code: ExecTimeout, Msg: "Invalid timeout: must be a finite number of seconds"}
	}

	config, err := e.shellConfigFor()
	if err != nil {
		return res, err
	}

	cwd := e.cwd
	if opts.Cwd != "" {
		cwd = e.resolve(opts.Cwd)
	}
	if _, statErr := os.Stat(cwd); statErr != nil {
		return res, &ExecutionError{
			Code: ExecSpawnError,
			Msg:  fmt.Sprintf("Working directory does not exist: %s\nCannot execute bash commands.", cwd),
			Err:  statErr,
		}
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout*float64(time.Second)))
		defer cancel()
	}

	cmd := exec.Command(config.shell, append(config.args, command)...)
	cmd.Dir = cwd
	cmd.Env = e.shellEnvFor(opts.Env, opts.InheritEnv == nil || *opts.InheritEnv)
	setProcessGroup(cmd)

	var stdout, stderr strings.Builder
	cmd.Stdout = streamWriter{&stdout, opts.OnStdout}
	cmd.Stderr = streamWriter{&stderr, opts.OnStderr}

	if err := cmd.Start(); err != nil {
		return res, &ExecutionError{Code: ExecSpawnError, Msg: err.Error(), Err: err}
	}
	e.mu.Lock()
	e.activeChildren[cmd] = true
	e.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	timedOut := false
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		killProcessTree(cmd)
		waitErr = <-done
	}

	e.mu.Lock()
	delete(e.activeChildren, cmd)
	e.mu.Unlock()

	res = ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if timedOut {
		return res, &ExecutionError{Code: ExecTimeout, Msg: fmt.Sprintf("timeout:%v", opts.Timeout), Err: ctx.Err()}
	}
	if ctx.Err() != nil {
		return res, &ExecutionError{Code: ExecAborted, Msg: "aborted", Err: ctx.Err()}
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, &ExecutionError{Code: ExecSpawnError, Msg: waitErr.Error(), Err: waitErr}
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
