package tools

import (
	"fmt"
	"strings"
)

// diffPart is one run of a line-level diff: unchanged, added or removed
// lines. Lines keep their trailing newlines, mirroring jsdiff's diffLines.
type diffPart struct {
	added   bool
	removed bool
	lines   []string
}

// diffLines computes a line-level diff using Myers' O(ND) algorithm and
// groups the result into unchanged/removed/added runs, with removed runs
// emitted before the added run of the same change block.
func diffLines(oldContent, newContent string) []diffPart {
	oldLines := splitLinesWithEndings(oldContent)
	newLines := splitLinesWithEndings(newContent)
	matches := myersMatches(oldLines, newLines)

	var parts []diffPart
	appendRun := func(added, removed bool, lines []string) {
		if len(lines) == 0 {
			return
		}
		if n := len(parts); n > 0 && parts[n-1].added == added && parts[n-1].removed == removed {
			parts[n-1].lines = append(parts[n-1].lines, lines...)
			return
		}
		parts = append(parts, diffPart{added: added, removed: removed, lines: append([]string{}, lines...)})
	}

	i, j := 0, 0
	for _, m := range append(matches, [2]int{len(oldLines), len(newLines)}) {
		appendRun(false, true, oldLines[i:m[0]])
		appendRun(true, false, newLines[j:m[1]])
		if m[0] < len(oldLines) {
			appendRun(false, false, oldLines[m[0]:m[0]+1])
		}
		i, j = m[0]+1, m[1]+1
	}
	return parts
}

// myersMatches returns the matched line pairs (oldIndex, newIndex) of the
// longest common subsequence between a and b.
func myersMatches(a, b []string) [][2]int {
	n, m := len(a), len(b)
	maxD := n + m
	if maxD == 0 {
		return nil
	}
	v := map[int]int{1: 0}
	trace := make([]map[int]int, 0, maxD+1)

	var solvedD int
	for d := 0; d <= maxD; d++ {
		vd := make(map[int]int, len(v))
		for k, x := range v {
			vd[k] = x
		}
		trace = append(trace, vd)
		found := false
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[k] = x
			if x >= n && y >= m {
				found = true
				solvedD = d
				break
			}
		}
		if found {
			break
		}
	}

	// Backtrack the edit path, recording matched (diagonal) steps.
	var matches [][2]int
	x, y := n, m
	for d := solvedD; d > 0 && (x > 0 || y > 0); d-- {
		vd := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vd[k-1] < vd[k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vd[prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			if x > 0 && y > 0 {
				matches = append(matches, [2]int{x - 1, y - 1})
			}
			x--
			y--
		}
		if d > 0 {
			if prevK == k+1 {
				y = prevY
				x = prevX
			} else {
				x = prevX
				y = prevY
			}
		}
	}
	for x > 0 && y > 0 {
		matches = append(matches, [2]int{x - 1, y - 1})
		x--
		y--
	}
	// Reverse into ascending order.
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	return matches
}

// generateUnifiedPatch renders a standard unified patch with file headers.
func generateUnifiedPatch(path, oldContent, newContent string, contextLines int) string {
	parts := diffLines(oldContent, newContent)

	type hunkLine struct {
		prefix byte
		text   string // line without trailing newline
		noEOL  bool
	}
	var all []hunkLine
	push := func(prefix byte, line string) {
		text, hadNewline := strings.CutSuffix(line, "\n")
		all = append(all, hunkLine{prefix: prefix, text: text, noEOL: !hadNewline})
	}
	for _, part := range parts {
		prefix := byte(' ')
		if part.added {
			prefix = '+'
		} else if part.removed {
			prefix = '-'
		}
		for _, line := range part.lines {
			push(prefix, line)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", path, path)

	oldLine, newLine := 1, 1
	i := 0
	for i < len(all) {
		// Skip unchanged runs, keeping context.
		if all[i].prefix == ' ' {
			j := i
			for j < len(all) && all[j].prefix == ' ' {
				j++
			}
			if j == len(all) {
				break
			}
			skip := j - i
			keep := min(skip, contextLines)
			oldLine += skip - keep
			newLine += skip - keep
			i = j - keep
		}

		// Collect one hunk: changes plus surrounding context, merging change
		// blocks separated by <= 2*contextLines unchanged lines.
		hunkStart := i
		end := i
		for {
			for end < len(all) && all[end].prefix != ' ' {
				end++
			}
			gap := end
			for gap < len(all) && all[gap].prefix == ' ' {
				gap++
			}
			if gap < len(all) && gap-end <= contextLines*2 {
				end = gap
				continue
			}
			end += min(gap-end, contextLines)
			break
		}

		hunkOldStart, hunkNewStart := oldLine, newLine
		oldCount, newCount := 0, 0
		var lines []string
		for _, hl := range all[hunkStart:end] {
			lines = append(lines, string(hl.prefix)+hl.text)
			if hl.noEOL {
				lines = append(lines, `\ No newline at end of file`)
			}
			switch hl.prefix {
			case ' ':
				oldCount++
				newCount++
			case '-':
				oldCount++
			case '+':
				newCount++
			}
		}
		oldLine += oldCount
		newLine += newCount

		fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(hunkOldStart, oldCount), hunkRange(hunkNewStart, newCount))
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		i = end
	}
	return b.String()
}

func hunkRange(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	if count == 0 {
		start--
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// generateDiffString renders a display-oriented diff with line numbers and
// context, returning the first changed line number in the new file.
func generateDiffString(oldContent, newContent string, contextLines int) (diff string, firstChangedLine int) {
	parts := diffLines(oldContent, newContent)
	var output []string

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	lineNumWidth := len(fmt.Sprintf("%d", max(len(oldLines), len(newLines))))
	pad := func(n int) string { return fmt.Sprintf("%*d", lineNumWidth, n) }
	gapMarker := fmt.Sprintf(" %*s ...", lineNumWidth, "")

	oldLineNum, newLineNum := 1, 1
	lastWasChange := false
	firstChangedLine = 0

	for i, part := range parts {
		raw := make([]string, len(part.lines))
		for j, line := range part.lines {
			raw[j] = strings.TrimSuffix(line, "\n")
		}

		if part.added || part.removed {
			if firstChangedLine == 0 {
				firstChangedLine = newLineNum
			}
			for _, line := range raw {
				if part.added {
					output = append(output, fmt.Sprintf("+%s %s", pad(newLineNum), line))
					newLineNum++
				} else {
					output = append(output, fmt.Sprintf("-%s %s", pad(oldLineNum), line))
					oldLineNum++
				}
			}
			lastWasChange = true
			continue
		}

		nextPartIsChange := i < len(parts)-1 && (parts[i+1].added || parts[i+1].removed)
		hasLeadingChange := lastWasChange
		hasTrailingChange := nextPartIsChange
		emit := func(line string) {
			output = append(output, fmt.Sprintf(" %s %s", pad(oldLineNum), line))
			oldLineNum++
			newLineNum++
		}

		switch {
		case hasLeadingChange && hasTrailingChange:
			if len(raw) <= contextLines*2 {
				for _, line := range raw {
					emit(line)
				}
			} else {
				for _, line := range raw[:contextLines] {
					emit(line)
				}
				skipped := len(raw) - 2*contextLines
				output = append(output, gapMarker)
				oldLineNum += skipped
				newLineNum += skipped
				for _, line := range raw[len(raw)-contextLines:] {
					emit(line)
				}
			}
		case hasLeadingChange:
			shown := raw[:min(contextLines, len(raw))]
			for _, line := range shown {
				emit(line)
			}
			if skipped := len(raw) - len(shown); skipped > 0 {
				output = append(output, gapMarker)
				oldLineNum += skipped
				newLineNum += skipped
			}
		case hasTrailingChange:
			skipped := max(0, len(raw)-contextLines)
			if skipped > 0 {
				output = append(output, gapMarker)
				oldLineNum += skipped
				newLineNum += skipped
			}
			for _, line := range raw[skipped:] {
				emit(line)
			}
		default:
			oldLineNum += len(raw)
			newLineNum += len(raw)
		}
		lastWasChange = false
	}

	return strings.Join(output, "\n"), firstChangedLine
}
