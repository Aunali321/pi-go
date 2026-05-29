package llm

import (
	"strings"
	"unicode/utf8"
)

func sanitizeSurrogates(s string) string {
	if utf8.ValidString(s) {
		ok := true
		for _, r := range s {
			if r >= 0xD800 && r <= 0xDFFF {
				ok = false
				break
			}
		}
		if ok {
			return s
		}
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r >= 0xD800 && r <= 0xDFFF {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}
