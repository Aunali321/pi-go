// dumpclassify mirrors parity/classify-cmp.mjs: overflow and retryable
// verdicts for a shared table of provider error strings.
package main

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/aunali321/pi-go/llm"
)

func msg(stopReason llm.StopReason, errorMessage string, usage llm.Usage) *llm.AssistantMessage {
	return &llm.AssistantMessage{
		API: "openai-completions", Provider: "openrouter", Model: "m",
		Usage: usage, StopReason: stopReason, ErrorMessage: errorMessage, Timestamp: time.UnixMilli(1),
	}
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	var errors []string
	if err := json.Unmarshal(data, &errors); err != nil {
		panic(err)
	}

	overflow := map[string]bool{}
	retryable := map[string]bool{}
	for _, e := range errors {
		overflow[e] = llm.IsContextOverflow(msg(llm.StopError, e, llm.Usage{}), 200000)
		retryable[e] = llm.IsRetryableAssistantError(msg(llm.StopError, e, llm.Usage{}))
	}
	overflow["<silent: stop, input 210k>"] = llm.IsContextOverflow(msg(llm.StopEnd, "", llm.Usage{Input: 190000, CacheRead: 20000}), 200000)
	overflow["<silent: stop, input 100k>"] = llm.IsContextOverflow(msg(llm.StopEnd, "", llm.Usage{Input: 100000}), 200000)
	overflow["<length-stop: zero output, full window>"] = llm.IsContextOverflow(msg(llm.StopLength, "", llm.Usage{Input: 199000}), 200000)
	overflow["<length-stop: with output>"] = llm.IsContextOverflow(msg(llm.StopLength, "", llm.Usage{Input: 199000, Output: 50}), 200000)

	b, _ := json.MarshalIndent(map[string]any{"overflow": overflow, "retryable": retryable}, "", "  ")
	os.Stdout.Write(b)
}
