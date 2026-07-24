package llm

import (
	"fmt"
	"strings"
)

// grammarSampling is a resolved grammar-constrained tool: which grammar
// variant to send and which single string property carries the raw input.
type grammarSampling struct {
	format        string // "lark" or "regex"
	definition    string
	inputProperty string
}

// grammarInputBuffer tracks the synthesized JSON argument stream for a
// grammar (custom) tool call: raw provider input is wrapped on the fly into
// `{"<property>":"<escaped input>"}` deltas.
type grammarInputBuffer struct {
	input   string
	started bool
	closed  bool
}

func getGrammarToolInput(toolName string, args map[string]any, inputProperty string) (string, error) {
	input, ok := args[inputProperty].(string)
	if !ok {
		return "", fmt.Errorf("Grammar tool call %q requires argument %q to be a string.", toolName, inputProperty)
	}
	return input, nil
}

// appendGrammarInputDelta advances the buffer to nextInput and returns the
// synthesized JSON delta. ok is false when there is nothing to emit.
func appendGrammarInputDelta(buf *grammarInputBuffer, inputProperty, nextInput string, close bool) (delta string, ok bool, err error) {
	if buf.closed {
		if close && nextInput == buf.input {
			return "", false, nil
		}
		return "", false, fmt.Errorf("grammar tool input for property %q changed after it was closed", inputProperty)
	}
	if !strings.HasPrefix(nextInput, buf.input) {
		return "", false, fmt.Errorf("grammar tool input for property %q changed non-monotonically", inputProperty)
	}

	inputDelta := nextInput[len(buf.input):]
	if !close && inputDelta == "" {
		return "", false, nil
	}

	var b strings.Builder
	if !buf.started {
		b.WriteString("{" + jsonString(inputProperty) + `:"`)
		buf.started = true
	}
	b.WriteString(jsonStringInner(inputDelta))
	buf.input = nextInput

	if close {
		b.WriteString(`"}`)
		buf.closed = true
	}
	return b.String(), true, nil
}

func inferGrammarInputProperty(t Tool) (string, error) {
	if t.Parameters["type"] != "object" {
		return "", fmt.Errorf("grammar constrained sampling requires an object parameter schema")
	}
	required := stringSlice(t.Parameters["required"])
	if len(required) != 1 {
		return "", fmt.Errorf("grammar constrained sampling requires exactly one required string property")
	}
	inputProperty := required[0]
	properties, _ := t.Parameters["properties"].(map[string]any)
	property, _ := properties[inputProperty].(map[string]any)
	if property == nil {
		return "", fmt.Errorf("grammar constrained sampling requires a properties entry for %s", inputProperty)
	}
	if property["type"] != "string" {
		return "", fmt.Errorf("grammar constrained sampling property %s must have type string", inputProperty)
	}
	return inputProperty, nil
}

// resolveJSONSchemaStrict returns whether a tool must be sent with
// strict: true. It errors when the tool requires strict sampling but the
// provider does not support it.
func resolveJSONSchemaStrict(t Tool, supportsStrictMode bool) (bool, error) {
	cs := t.ConstrainedSampling
	if cs == nil || cs.Type != "json_schema" {
		return false, nil
	}
	if supportsStrictMode {
		return true, nil
	}
	if cs.Strict == "require" {
		return false, fmt.Errorf("Tool %q requires JSON-schema constrained sampling, but strict tools are unsupported.", t.Name)
	}
	return false, nil
}

func resolveGrammarSampling(t Tool, supportsOpenAIGrammarTools bool) (*grammarSampling, error) {
	cs := t.ConstrainedSampling
	if cs == nil || cs.Type != "grammar" || !supportsOpenAIGrammarTools {
		return nil, nil
	}

	lark := cs.Variants[GrammarOpenAILark]
	regex := cs.Variants[GrammarOpenAIRegex]
	hasLark := strings.TrimSpace(lark) != ""
	hasRegex := strings.TrimSpace(regex) != ""
	if !hasLark && !hasRegex {
		return nil, fmt.Errorf("Tool %q cannot use grammar constrained sampling: no supported grammar variant was provided.", t.Name)
	}

	inputProperty, err := inferGrammarInputProperty(t)
	if err != nil {
		return nil, fmt.Errorf("Tool %q cannot use grammar constrained sampling: %s.", t.Name, err)
	}
	g := &grammarSampling{format: "lark", definition: lark, inputProperty: inputProperty}
	if !hasLark {
		g.format, g.definition = "regex", regex
	}
	return g, nil
}

// grammarInputProperties maps grammar-constrained tool names to the argument
// property carrying their raw input.
func grammarInputProperties(tools []Tool, supportsOpenAIGrammarTools bool) (map[string]string, error) {
	var properties map[string]string
	for _, t := range tools {
		g, err := resolveGrammarSampling(t, supportsOpenAIGrammarTools)
		if err != nil {
			return nil, err
		}
		if g != nil {
			if properties == nil {
				properties = map[string]string{}
			}
			properties[t.Name] = g.inputProperty
		}
	}
	return properties, nil
}

func stringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			str, ok := e.(string)
			if !ok {
				return nil
			}
			out = append(out, str)
		}
		return out
	}
	return nil
}
