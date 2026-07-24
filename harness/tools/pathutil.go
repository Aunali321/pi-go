package tools

import (
	"context"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/aunali321/pi-go/harness/env"
)

var unicodeSpaces = regexp.MustCompile(`[\x{00A0}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`)

const narrowNoBreakSpace = " "

func normalizeToolPath(path string) string {
	normalized := unicodeSpaces.ReplaceAllString(path, " ")
	return strings.TrimPrefix(normalized, "@")
}

// ResolveToolPath normalizes model-provided path quirks (Unicode spaces, a
// leading @) and resolves the result to an absolute path.
func ResolveToolPath(ctx context.Context, execEnv env.ExecutionEnv, path string) (string, error) {
	return execEnv.AbsolutePath(ctx, normalizeToolPath(path))
}

var amPMPattern = regexp.MustCompile(`(?i) (AM|PM)\.`)

// ResolveReadToolPath resolves a path for reading, additionally probing
// Unicode filename variants (narrow no-break spaces before AM/PM, NFD
// normalization, curly apostrophes) that screenshots and macOS filenames
// commonly use.
func ResolveReadToolPath(ctx context.Context, execEnv env.ExecutionEnv, path string) (string, error) {
	resolved, err := ResolveToolPath(ctx, execEnv, path)
	if err != nil {
		return "", err
	}
	variants := []string{
		resolved,
		amPMPattern.ReplaceAllString(resolved, narrowNoBreakSpace+"$1."),
		norm.NFD.String(resolved),
		strings.ReplaceAll(resolved, "'", "’"),
		strings.ReplaceAll(norm.NFD.String(resolved), "'", "’"),
	}
	seen := map[string]bool{}
	for _, variant := range variants {
		if seen[variant] {
			continue
		}
		seen[variant] = true
		exists, err := execEnv.Exists(ctx, variant)
		if err != nil {
			return "", err
		}
		if exists {
			return variant, nil
		}
	}
	return resolved, nil
}
