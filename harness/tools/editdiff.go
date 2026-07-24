package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Edit is one exact-text replacement.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 || crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

func normalizeToLF(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

const utf8BOM = "\uFEFF"

func stripBOM(content string) (bom, text string) {
	if strings.HasPrefix(content, utf8BOM) {
		return utf8BOM, content[len(utf8BOM):]
	}
	return "", content
}

var (
	smartSingleQuotes = regexp.MustCompile(`[\x{2018}\x{2019}\x{201A}\x{201B}]`)
	smartDoubleQuotes = regexp.MustCompile(`[\x{201C}\x{201D}\x{201E}\x{201F}]`)
	unicodeDashes     = regexp.MustCompile(`[\x{2010}\x{2011}\x{2012}\x{2013}\x{2014}\x{2015}\x{2212}]`)
	specialSpaces     = regexp.MustCompile(`[\x{00A0}\x{2002}-\x{200A}\x{202F}\x{205F}\x{3000}]`)
)

// normalizeForFuzzyMatch applies progressive transformations for fuzzy
// matching: NFKC, trailing-whitespace stripping per line, and ASCII
// normalization of smart quotes, Unicode dashes and special spaces.
func normalizeForFuzzyMatch(text string) string {
	s := norm.NFKC.String(text)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	s = strings.Join(lines, "\n")
	s = smartSingleQuotes.ReplaceAllString(s, "'")
	s = smartDoubleQuotes.ReplaceAllString(s, `"`)
	s = unicodeDashes.ReplaceAllString(s, "-")
	s = specialSpaces.ReplaceAllString(s, " ")
	return s
}

// splitLinesWithEndings splits content into lines, each keeping its trailing
// newline (the final line may lack one).
func splitLinesWithEndings(content string) []string {
	if content == "" {
		return nil
	}
	var lines []string
	for {
		idx := strings.IndexByte(content, '\n')
		if idx == -1 {
			lines = append(lines, content)
			break
		}
		lines = append(lines, content[:idx+1])
		content = content[idx+1:]
		if content == "" {
			break
		}
	}
	return lines
}

type lineSpan struct{ start, end int }

type textReplacement struct {
	matchIndex  int
	matchLength int
	newText     string
}

type matchedEdit struct {
	editIndex int
	textReplacement
}

func getLineSpans(content string) []lineSpan {
	offset := 0
	lines := splitLinesWithEndings(content)
	spans := make([]lineSpan, len(lines))
	for i, line := range lines {
		spans[i] = lineSpan{start: offset, end: offset + len(line)}
		offset += len(line)
	}
	return spans
}

func getReplacementLineRange(lines []lineSpan, r textReplacement) (startLine, endLine int, err error) {
	replacementStart := r.matchIndex
	replacementEnd := r.matchIndex + r.matchLength

	startLine = -1
	for i, line := range lines {
		if replacementStart >= line.start && replacementStart < line.end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, fmt.Errorf("Replacement range is outside the base content.")
	}

	endLine = startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, fmt.Errorf("Replacement range is outside the base content.")
	}
	return startLine, endLine + 1, nil
}

func applyReplacements(content string, replacements []textReplacement, offset int) string {
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		matchIndex := r.matchIndex - offset
		result = result[:matchIndex] + r.newText + result[matchIndex+r.matchLength:]
	}
	return result
}

// applyReplacementsPreservingUnchangedLines applies replacements matched
// against baseContent (a normalized view of originalContent) while copying
// unchanged line blocks back from the original.
func applyReplacementsPreservingUnchangedLines(originalContent, baseContent string, replacements []textReplacement) (string, error) {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", fmt.Errorf("Cannot preserve unchanged lines because the base content has a different line count.")
	}

	type group struct {
		startLine, endLine int
		replacements       []textReplacement
	}
	var groups []*group
	sorted := append([]textReplacement{}, replacements...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].matchIndex < sorted[j].matchIndex })
	for _, r := range sorted {
		startLine, endLine, err := getReplacementLineRange(baseLines, r)
		if err != nil {
			return "", err
		}
		if len(groups) > 0 {
			current := groups[len(groups)-1]
			if startLine < current.endLine {
				current.endLine = max(current.endLine, endLine)
				current.replacements = append(current.replacements, r)
				continue
			}
		}
		groups = append(groups, &group{startLine: startLine, endLine: endLine, replacements: []textReplacement{r}})
	}

	originalLineIndex := 0
	var result strings.Builder
	for _, g := range groups {
		result.WriteString(strings.Join(originalLines[originalLineIndex:g.startLine], ""))
		groupStart := baseLines[g.startLine].start
		groupEnd := baseLines[g.endLine-1].end
		result.WriteString(applyReplacements(baseContent[groupStart:groupEnd], g.replacements, groupStart))
		originalLineIndex = g.endLine
	}
	result.WriteString(strings.Join(originalLines[originalLineIndex:], ""))
	return result.String(), nil
}

type fuzzyMatchResult struct {
	found         bool
	index         int
	matchLength   int
	usedFuzzy     bool
	contentForRep string
}

// fuzzyFindText finds oldText in content, trying exact match first, then
// fuzzy match in normalized space.
func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	if exactIndex := strings.Index(content, oldText); exactIndex != -1 {
		return fuzzyMatchResult{found: true, index: exactIndex, matchLength: len(oldText), contentForRep: content}
	}

	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return fuzzyMatchResult{contentForRep: content}
	}
	return fuzzyMatchResult{found: true, index: fuzzyIndex, matchLength: len(fuzzyOldText), usedFuzzy: true, contentForRep: fuzzyContent}
}

func countOccurrences(content, oldText string) int {
	return strings.Count(normalizeForFuzzyMatch(content), normalizeForFuzzyMatch(oldText))
}

func notFoundError(path string, editIndex, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
	}
	return fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", editIndex, path)
}

func duplicateError(path string, editIndex, totalEdits, occurrences int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occurrences, path)
	}
	return fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occurrences, editIndex, path)
}

func emptyOldTextError(path string, editIndex, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", editIndex, path)
}

func noChangeError(path string, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
}

// applyEditsToNormalizedContent applies one or more exact-text replacements
// to LF-normalized content. All edits match against the same original
// content; replacements apply in reverse order so offsets stay stable. If any
// edit needs fuzzy matching, the operation runs in fuzzy-normalized space and
// overlays line-level changes onto the original so unchanged lines keep their
// bytes.
func applyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (baseContent, newContent string, err error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, edit := range edits {
		normalizedEdits[i] = Edit{OldText: normalizeToLF(edit.OldText), NewText: normalizeToLF(edit.NewText)}
	}
	for i, edit := range normalizedEdits {
		if edit.OldText == "" {
			return "", "", emptyOldTextError(path, i, len(normalizedEdits))
		}
	}

	usedFuzzy := false
	for _, edit := range normalizedEdits {
		if fuzzyFindText(normalizedContent, edit.OldText).usedFuzzy {
			usedFuzzy = true
			break
		}
	}
	replacementBase := normalizedContent
	if usedFuzzy {
		replacementBase = normalizeForFuzzyMatch(normalizedContent)
	}

	var matchedEdits []matchedEdit
	for i, edit := range normalizedEdits {
		match := fuzzyFindText(replacementBase, edit.OldText)
		if !match.found {
			return "", "", notFoundError(path, i, len(normalizedEdits))
		}
		if occurrences := countOccurrences(replacementBase, edit.OldText); occurrences > 1 {
			return "", "", duplicateError(path, i, len(normalizedEdits), occurrences)
		}
		matchedEdits = append(matchedEdits, matchedEdit{
			editIndex:       i,
			textReplacement: textReplacement{matchIndex: match.index, matchLength: match.matchLength, newText: edit.NewText},
		})
	}

	sort.SliceStable(matchedEdits, func(i, j int) bool { return matchedEdits[i].matchIndex < matchedEdits[j].matchIndex })
	for i := 1; i < len(matchedEdits); i++ {
		previous, current := matchedEdits[i-1], matchedEdits[i]
		if previous.matchIndex+previous.matchLength > current.matchIndex {
			return "", "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				previous.editIndex, current.editIndex, path)
		}
	}

	replacements := make([]textReplacement, len(matchedEdits))
	for i, m := range matchedEdits {
		replacements[i] = m.textReplacement
	}

	baseContent = normalizedContent
	if usedFuzzy {
		newContent, err = applyReplacementsPreservingUnchangedLines(normalizedContent, replacementBase, replacements)
		if err != nil {
			return "", "", err
		}
	} else {
		newContent = applyReplacements(replacementBase, replacements, 0)
	}

	if baseContent == newContent {
		return "", "", noChangeError(path, len(normalizedEdits))
	}
	return baseContent, newContent, nil
}
