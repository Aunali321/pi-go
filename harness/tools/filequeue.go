package tools

import (
	"context"
	"errors"
	"sync"

	"github.com/aunali321/pi-go/harness/env"
)

var (
	mutationMu     sync.Mutex
	mutationQueues = map[env.ExecutionEnv]map[string]*sync.Mutex{}
)

func mutationQueueKey(ctx context.Context, execEnv env.ExecutionEnv, path string) (string, error) {
	absolutePath, err := execEnv.AbsolutePath(ctx, path)
	if err != nil {
		return "", err
	}
	canonical, err := execEnv.CanonicalPath(ctx, absolutePath)
	if err == nil {
		return canonical, nil
	}
	var fe *env.FileError
	if errors.As(err, &fe) && (fe.Code == env.FileNotFound || fe.Code == env.FileNotSupported) {
		return absolutePath, nil
	}
	return "", err
}

// withFileMutationQueue serializes file mutations targeting the same
// environment and canonical path.
func withFileMutationQueue[T any](ctx context.Context, execEnv env.ExecutionEnv, path string, fn func() (T, error)) (T, error) {
	var zero T
	key, err := mutationQueueKey(ctx, execEnv, path)
	if err != nil {
		return zero, err
	}

	mutationMu.Lock()
	queues := mutationQueues[execEnv]
	if queues == nil {
		queues = map[string]*sync.Mutex{}
		mutationQueues[execEnv] = queues
	}
	mu := queues[key]
	if mu == nil {
		mu = &sync.Mutex{}
		queues[key] = mu
	}
	mutationMu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	return fn()
}
