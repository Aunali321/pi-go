package harness

import (
	"context"

	"github.com/aunali321/pi-go/harness/env"
	"regexp"
	"sort"
	"strings"
)

// Skill is loaded from a SKILL.md file or supplied by an application.
type Skill struct {
	Name                   string
	Description            string
	Content                string
	FilePath               string
	DisableModelInvocation bool
}

// PromptTemplate can be formatted into a prompt for explicit invocation.
type PromptTemplate struct {
	Name        string
	Description string
	Content     string
}

// FormatSkillInvocation formats a skill invocation prompt.
func FormatSkillInvocation(skill Skill, additionalInstructions string) string {
	block := "<skill name=\"" + skill.Name + "\" location=\"" + skill.FilePath + "\">\n" +
		"References are relative to " + dirnameEnvPath(skill.FilePath) + ".\n\n" +
		skill.Content + "\n</skill>"
	if additionalInstructions != "" {
		return block + "\n\n" + additionalInstructions
	}
	return block
}

// SkillDiagnostic is a warning produced while loading skills.
type SkillDiagnostic struct {
	Code    string
	Message string
	Path    string
}

const (
	maxNameLength        = 64
	maxDescriptionLength = 1024
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// LoadSkills loads skills from one or more directories, traversing recursively,
// loading SKILL.md files and direct root .md files.
func LoadSkills(ctx context.Context, fs env.ExecutionEnv, dirs ...string) ([]Skill, []SkillDiagnostic) {
	var skills []Skill
	var diags []SkillDiagnostic
	for _, dir := range dirs {
		info, err := fs.FileInfo(ctx, dir)
		if err != nil {
			if !isNotFound(err) {
				diags = append(diags, SkillDiagnostic{"file_info_failed", err.Error(), dir})
			}
			continue
		}
		if info.Kind != env.KindDirectory {
			continue
		}
		s, d := loadSkillsFromDir(ctx, fs, info.Path, true, info.Path)
		skills = append(skills, s...)
		diags = append(diags, d...)
	}
	return skills, diags
}

func loadSkillsFromDir(ctx context.Context, fs env.ExecutionEnv, dir string, includeRootFiles bool, rootDir string) ([]Skill, []SkillDiagnostic) {
	var skills []Skill
	var diags []SkillDiagnostic

	entries, err := fs.ListDir(ctx, dir)
	if err != nil {
		diags = append(diags, SkillDiagnostic{"list_failed", err.Error(), dir})
		return skills, diags
	}

	for _, entry := range entries {
		if entry.Name == "SKILL.md" && entry.Kind == env.KindFile {
			skill, d := loadSkillFromFile(ctx, fs, entry.Path)
			if skill != nil {
				skills = append(skills, *skill)
			}
			diags = append(diags, d...)
			return skills, diags
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules" {
			continue
		}
		if entry.Kind == env.KindDirectory {
			s, d := loadSkillsFromDir(ctx, fs, entry.Path, false, rootDir)
			skills = append(skills, s...)
			diags = append(diags, d...)
			continue
		}
		if entry.Kind != env.KindFile || !includeRootFiles || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		skill, d := loadSkillFromFile(ctx, fs, entry.Path)
		if skill != nil {
			skills = append(skills, *skill)
		}
		diags = append(diags, d...)
	}
	return skills, diags
}

func loadSkillFromFile(ctx context.Context, fs env.ExecutionEnv, filePath string) (*Skill, []SkillDiagnostic) {
	var diags []SkillDiagnostic
	raw, err := fs.ReadTextFile(ctx, filePath)
	if err != nil {
		return nil, []SkillDiagnostic{{"read_failed", err.Error(), filePath}}
	}
	frontmatter, body := parseFrontmatter(raw)

	skillDir := dirnameEnvPath(filePath)
	parentDirName := basenameEnvPath(skillDir)
	description := frontmatter["description"]

	for _, e := range validateDescription(description) {
		diags = append(diags, SkillDiagnostic{"invalid_metadata", e, filePath})
	}
	name := frontmatter["name"]
	if name == "" {
		name = parentDirName
	}
	for _, e := range validateName(name, parentDirName) {
		diags = append(diags, SkillDiagnostic{"invalid_metadata", e, filePath})
	}
	if strings.TrimSpace(description) == "" {
		return nil, diags
	}
	return &Skill{
		Name:                   name,
		Description:            description,
		Content:                body,
		FilePath:               filePath,
		DisableModelInvocation: frontmatter["disable-model-invocation"] == "true",
	}, diags
}

func validateName(name, parentDirName string) []string {
	var errs []string
	if name != parentDirName {
		errs = append(errs, "name \""+name+"\" does not match parent directory \""+parentDirName+"\"")
	}
	if len(name) > maxNameLength {
		errs = append(errs, "name exceeds 64 characters")
	}
	if !skillNameRe.MatchString(name) {
		errs = append(errs, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errs = append(errs, "name must not start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		errs = append(errs, "name must not contain consecutive hyphens")
	}
	return errs
}

func validateDescription(description string) []string {
	if strings.TrimSpace(description) == "" {
		return []string{"description is required"}
	}
	if len(description) > maxDescriptionLength {
		return []string{"description exceeds 1024 characters"}
	}
	return nil
}

func isNotFound(err error) bool {
	if fe, ok := err.(*env.FileError); ok {
		return fe.Code == env.FileNotFound
	}
	return false
}

// parseFrontmatter parses a simple YAML frontmatter block (scalar key: value
// pairs) and returns the remaining body.
func parseFrontmatter(content string) (map[string]string, string) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	frontmatter := map[string]string{}
	if !strings.HasPrefix(normalized, "---") {
		return frontmatter, normalized
	}
	end := strings.Index(normalized[3:], "\n---")
	if end == -1 {
		return frontmatter, normalized
	}
	end += 3
	yamlBlock := normalized[4:end]
	body := strings.TrimSpace(normalized[end+4:])
	for _, line := range strings.Split(yamlBlock, "\n") {
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		frontmatter[key] = val
	}
	return frontmatter, body
}

func dirnameEnvPath(path string) string {
	normalized := strings.TrimRight(path, "/")
	idx := strings.LastIndex(normalized, "/")
	if idx <= 0 {
		return "/"
	}
	return normalized[:idx]
}

func basenameEnvPath(path string) string {
	normalized := strings.TrimRight(path, "/")
	idx := strings.LastIndex(normalized, "/")
	if idx == -1 {
		return normalized
	}
	return normalized[idx+1:]
}
