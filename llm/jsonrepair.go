package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

var validJSONEscapes = map[byte]bool{
	'"': true, '\\': true, '/': true, 'b': true,
	'f': true, 'n': true, 'r': true, 't': true, 'u': true,
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func escapeControl(c byte) string {
	switch c {
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	default:
		return fmt.Sprintf(`\u%04x`, c)
	}
}

// repairJSON escapes raw control characters inside strings and doubles
// backslashes that precede invalid escape characters.
func repairJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if !inString {
			b.WriteByte(c)
			if c == '"' {
				inString = true
			}
			continue
		}

		if c == '"' {
			b.WriteByte(c)
			inString = false
			continue
		}

		if c == '\\' {
			if i+1 >= len(s) {
				b.WriteString(`\\`)
				continue
			}
			next := s[i+1]
			if next == 'u' && i+6 <= len(s) {
				digits := s[i+2 : i+6]
				if len(digits) == 4 && isHex(digits[0]) && isHex(digits[1]) && isHex(digits[2]) && isHex(digits[3]) {
					b.WriteString(`\u`)
					b.WriteString(digits)
					i += 5
					continue
				}
			}
			if validJSONEscapes[next] {
				b.WriteByte('\\')
				b.WriteByte(next)
				i++
				continue
			}
			b.WriteString(`\\`)
			continue
		}

		if c <= 0x1f {
			b.WriteString(escapeControl(c))
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func parseJSONWithRepair(s string) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out, nil
	}
	repaired := repairJSON(s)
	if repaired != s {
		if err := json.Unmarshal([]byte(repaired), &out); err == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("invalid json")
}

// parseStreamingJSON parses possibly-incomplete JSON accumulated during
// streaming, always returning a map (empty on total failure).
func parseStreamingJSON(partial string) map[string]any {
	if strings.TrimSpace(partial) == "" {
		return map[string]any{}
	}
	if out, err := parseJSONWithRepair(partial); err == nil {
		return out
	}
	if out := tryPartial(completePartialJSON(partial)); out != nil {
		return out
	}
	if out := tryPartial(completePartialJSON(repairJSON(partial))); out != nil {
		return out
	}
	return map[string]any{}
}

func tryPartial(s string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out
	}
	return nil
}

// completePartialJSON closes open strings, objects and arrays in a truncated
// JSON fragment so the result parses, trimming any trailing incomplete token.
func completePartialJSON(s string) string {
	var stack []byte
	inString := false
	escaped := false
	lastGood := 0

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
				lastGood = i + 1
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
			lastGood = i + 1
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			lastGood = i + 1
		case ',':
			lastGood = i
		case ':', ' ', '\t', '\n', '\r':
		default:
			lastGood = i + 1
		}
	}

	out := strings.TrimRight(s[:lastGood], " \t\n\r,:")

	var b strings.Builder
	b.WriteString(out)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String()
}
