package harness

import (
	"context"

	"github.com/aunali321/pi-go/harness/env"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PromptTemplateDiagnostic is a warning produced while loading prompt templates.
type PromptTemplateDiagnostic struct {
	Code    string
	Message string
	Path    string
}

// LoadPromptTemplates loads templates from directories (direct .md children) or files.
func LoadPromptTemplates(ctx context.Context, fs env.ExecutionEnv, paths ...string) ([]PromptTemplate, []PromptTemplateDiagnostic) {
	var templates []PromptTemplate
	var diags []PromptTemplateDiagnostic
	for _, path := range paths {
		info, err := fs.FileInfo(ctx, path)
		if err != nil {
			if !isNotFound(err) {
				diags = append(diags, PromptTemplateDiagnostic{"file_info_failed", err.Error(), path})
			}
			continue
		}
		switch info.Kind {
		case env.KindDirectory:
			entries, err := fs.ListDir(ctx, info.Path)
			if err != nil {
				diags = append(diags, PromptTemplateDiagnostic{"list_failed", err.Error(), info.Path})
				continue
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			for _, entry := range entries {
				if entry.Kind != env.KindFile || !strings.HasSuffix(entry.Name, ".md") {
					continue
				}
				if tpl, d := loadTemplateFromFile(ctx, fs, entry.Path); tpl != nil {
					templates = append(templates, *tpl)
					diags = append(diags, d...)
				}
			}
		case env.KindFile:
			if strings.HasSuffix(info.Name, ".md") {
				if tpl, d := loadTemplateFromFile(ctx, fs, info.Path); tpl != nil {
					templates = append(templates, *tpl)
					diags = append(diags, d...)
				}
			}
		}
	}
	return templates, diags
}

func loadTemplateFromFile(ctx context.Context, fs env.ExecutionEnv, filePath string) (*PromptTemplate, []PromptTemplateDiagnostic) {
	raw, err := fs.ReadTextFile(ctx, filePath)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{{"read_failed", err.Error(), filePath}}
	}
	frontmatter, body := parseFrontmatter(raw)
	description := frontmatter["description"]
	if description == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" {
				if len(line) > 60 {
					description = line[:60] + "..."
				} else {
					description = line
				}
				break
			}
		}
	}
	name := strings.TrimSuffix(basenameEnvPath(filePath), ".md")
	return &PromptTemplate{Name: name, Description: description, Content: body}, nil
}

// ParseCommandArgs parses an argument string with simple shell-style quotes.
func ParseCommandArgs(argsString string) []string {
	var args []string
	var current strings.Builder
	var inQuote rune
	for _, char := range argsString {
		switch {
		case inQuote != 0:
			if char == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(char)
			}
		case char == '"' || char == '\'':
			inQuote = char
		case char == ' ' || char == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

var (
	reDollarNum   = regexp.MustCompile(`\$(\d+)`)
	reDollarSlice = regexp.MustCompile(`\$\{@:(\d+)(?::(\d+))?\}`)
)

// SubstituteArgs substitutes prompt template placeholders with arguments.
func SubstituteArgs(content string, args []string) string {
	result := reDollarNum.ReplaceAllStringFunc(content, func(m string) string {
		n, _ := strconv.Atoi(m[1:])
		if n-1 >= 0 && n-1 < len(args) {
			return args[n-1]
		}
		return ""
	})
	result = reDollarSlice.ReplaceAllStringFunc(result, func(m string) string {
		groups := reDollarSlice.FindStringSubmatch(m)
		start, _ := strconv.Atoi(groups[1])
		start--
		if start < 0 {
			start = 0
		}
		if groups[2] != "" {
			length, _ := strconv.Atoi(groups[2])
			end := start + length
			if start > len(args) {
				start = len(args)
			}
			if end > len(args) {
				end = len(args)
			}
			return strings.Join(args[start:end], " ")
		}
		if start > len(args) {
			start = len(args)
		}
		return strings.Join(args[start:], " ")
	})
	allArgs := strings.Join(args, " ")
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	result = strings.ReplaceAll(result, "$@", allArgs)
	return result
}

// FormatPromptTemplateInvocation formats a template with positional arguments.
func FormatPromptTemplateInvocation(template PromptTemplate, args []string) string {
	return SubstituteArgs(template.Content, args)
}
