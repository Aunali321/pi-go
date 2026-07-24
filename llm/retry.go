package llm

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// nonRetryableLimitPattern matches subscription/account limit errors that
// look like throttles but are deterministic: quota, budget and billing
// exhaustion must fail fast.
var nonRetryableLimitPattern = regexp.MustCompile(`(?i)` + strings.Join([]string{
	"GoUsageLimitError",
	"FreeUsageLimitError",
	"Monthly usage limit reached",
	"available balance",
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
}, "|"))

// retryablePattern matches transient provider, transport and stream errors.
var retryablePattern = regexp.MustCompile(`(?i)` + strings.Join([]string{
	"overloaded",
	"rate.?limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"524",
	"service.?unavailable",
	"server.?error",
	"internal.?error",
	"provider.?returned.?error",
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	"getaddrinfo",
	"ENOTFOUND",
	"EAI_AGAIN",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"socket connection was closed",
	"timed? out",
	"timeout",
	"terminated",
	"websocket.?closed",
	"websocket.?error",
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	"retry delay",
	"you can retry your request",
	"try your request again",
	"please retry your request",
	"ResourceExhausted",
}, "|"))

// RetryPolicy bounds assistant-call retries with exponential backoff
// (baseDelay * 2^(attempt-1)).
type RetryPolicy struct {
	Enabled bool
	// MaxRetries is the max retry attempts (0 = no retries). The initial
	// call never counts as a retry.
	MaxRetries int
	BaseDelay  time.Duration
}

// RetryCallbacks are optional hooks emitted by RetryAssistantCall around each
// retry attempt.
type RetryCallbacks struct {
	// OnRetryScheduled fires before the backoff sleep of each retry attempt
	// (1-indexed).
	OnRetryScheduled func(attempt, maxAttempts int, delay time.Duration, errorMessage string)
	// OnRetryAttemptStart fires after the backoff sleep, immediately before
	// the retried call starts.
	OnRetryAttemptStart func()
	// OnRetryFinished fires once when the loop ends: success if a later call
	// completed normally.
	OnRetryFinished func(success bool, attempt int, finalError string)
}

// IsRetryableAssistantError classifies whether a failed assistant message
// looks like a transient provider or transport error. Callers should handle
// context overflow separately first.
func IsRetryableAssistantError(m *AssistantMessage) bool {
	if m.StopReason != StopError || m.ErrorMessage == "" {
		return false
	}
	if nonRetryableLimitPattern.MatchString(m.ErrorMessage) {
		return false
	}
	return retryablePattern.MatchString(m.ErrorMessage)
}

// RetryAssistantCall runs a single assistant-producing call with bounded
// retry on transient errors. Aborted responses are terminal and never
// retried; non-retryable errors fail fast; ctx cancellation during backoff
// is normalized to an aborted AssistantMessage so callers do not need to
// care when cancellation happened. A nil or disabled policy runs produce
// once, unchanged.
func RetryAssistantCall(ctx context.Context, produce func() *AssistantMessage, policy *RetryPolicy, cb *RetryCallbacks) *AssistantMessage {
	maxAttempts := 0
	if policy != nil && policy.Enabled {
		maxAttempts = policy.MaxRetries
	}
	if cb == nil {
		cb = &RetryCallbacks{}
	}

	attempt := 0
	lastRetryAttempt := 0
	finish := func(success bool, finalError string) {
		if lastRetryAttempt > 0 && cb.OnRetryFinished != nil {
			cb.OnRetryFinished(success, lastRetryAttempt, finalError)
		}
	}

	for {
		response := produce()

		if response.StopReason == StopAborted {
			finish(false, "")
			return response
		}
		if response.StopReason != StopError {
			finish(true, "")
			return response
		}
		if attempt >= maxAttempts || !IsRetryableAssistantError(response) {
			finish(false, response.ErrorMessage)
			return response
		}

		attempt++
		lastRetryAttempt = attempt
		errorMessage := response.ErrorMessage
		if errorMessage == "" {
			errorMessage = "Unknown error"
		}
		delay := policy.BaseDelay * (1 << (attempt - 1))
		if cb.OnRetryScheduled != nil {
			cb.OnRetryScheduled(attempt, maxAttempts, delay, errorMessage)
		}

		select {
		case <-ctx.Done():
			finish(false, errorMessage)
			aborted := *response
			aborted.StopReason = StopAborted
			aborted.ErrorMessage = ""
			return &aborted
		case <-time.After(delay):
		}
		if cb.OnRetryAttemptStart != nil {
			cb.OnRetryAttemptStart()
		}
	}
}

const defaultMaxRetryDelay = 60 * time.Second

// providerHTTPError carries the HTTP status and headers of a failed provider
// request so the retry loop can honor server-driven retry hints.
type providerHTTPError struct {
	status  int
	headers http.Header
	message string
}

func (e *providerHTTPError) Error() string { return e.message }

// retryableProviderError mirrors the pinned OpenAI/Anthropic SDK retry
// policy: an explicit x-should-retry header wins, then 408/409/429 and 5xx.
// Transport errors (no status) are retryable.
func retryableProviderError(err error) bool {
	pe, ok := err.(*providerHTTPError)
	if !ok {
		return true
	}
	switch pe.headers.Get("x-should-retry") {
	case "true":
		return true
	case "false":
		return false
	}
	return pe.status == 408 || pe.status == 409 || pe.status == 429 || pe.status >= 500
}

func validateServerRetryDelay(delay, maxDelay time.Duration, errorMessage string) (time.Duration, error) {
	if maxDelay > 0 && delay > maxDelay {
		return 0, fmt.Errorf("Server requested %ds retry delay (max: %ds). %s",
			int64(math.Ceil(delay.Seconds())), int64(math.Ceil(maxDelay.Seconds())), errorMessage)
	}
	return delay, nil
}

func providerRetryDelay(err error, retryIndex int, maxDelay time.Duration) (time.Duration, error) {
	if pe, ok := err.(*providerHTTPError); ok {
		if v := pe.headers.Get("retry-after-ms"); v != "" {
			if ms, parseErr := strconv.ParseFloat(v, 64); parseErr == nil {
				return validateServerRetryDelay(time.Duration(ms*float64(time.Millisecond)), maxDelay, pe.message)
			}
		}
		if v := pe.headers.Get("retry-after"); v != "" {
			if secs, parseErr := strconv.ParseFloat(v, 64); parseErr == nil {
				return validateServerRetryDelay(time.Duration(secs*float64(time.Second)), maxDelay, pe.message)
			}
			if at, parseErr := http.ParseTime(v); parseErr == nil {
				return validateServerRetryDelay(time.Until(at), maxDelay, pe.message)
			}
		}
	}
	exponential := math.Min(0.5*math.Pow(2, float64(retryIndex)), 8)
	jittered := exponential * (1 - rand.Float64()*0.25)
	return time.Duration(jittered * float64(time.Second)), nil
}

// retryProviderRequest reproduces the retry behavior of the OpenAI/Anthropic
// SDKs with an interruptible backoff sleep. maxDelay caps server-requested
// delays (default 60s); a negative maxDelay disables the cap.
func retryProviderRequest[T any](ctx context.Context, request func() (T, error), maxRetries int, maxDelay time.Duration) (T, error) {
	if maxDelay == 0 {
		maxDelay = defaultMaxRetryDelay
	} else if maxDelay < 0 {
		maxDelay = 0
	}

	retriesRemaining := maxRetries
	for {
		result, err := request()
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return result, err
		}
		if retriesRemaining <= 0 || !retryableProviderError(err) {
			return result, err
		}

		retryIndex := maxRetries - retriesRemaining
		retriesRemaining--
		delay, delayErr := providerRetryDelay(err, retryIndex, maxDelay)
		if delayErr != nil {
			return result, delayErr
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(delay):
		}
	}
}
