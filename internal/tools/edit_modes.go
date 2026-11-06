package tools

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

type patchEditEntry struct {
	Op     string `json:"op"`
	Rename string `json:"rename"`
	Diff   string `json:"diff"`
}

type patchHunkLine struct {
	kind byte
	text string
}

func (tool EditTool) applyPatch(ctx context.Context, execution ExecutionContext, args editArguments) (ToolResult, error) {
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	resolved, err := workspace.Resolve(args.Path)
	if err != nil {
		return ToolResult{}, err
	}
	before, statErr := SnapshotFile(resolved.Absolute)
	if statErr != nil && !os.IsNotExist(statErr) {
		return ToolResult{}, statErr
	}
	if args.ExpectedSHA256 != "" {
		if statErr != nil || !strings.EqualFold(args.ExpectedSHA256, before.SHA256) {
			return ToolResult{}, fmt.Errorf("tools: file %q has changed", args.Path)
		}
	}
	current := ""
	if statErr == nil {
		data, _, readErr := readTextFile(resolved.Absolute)
		if readErr != nil {
			return ToolResult{}, readErr
		}
		current = string(data)
	}
	moveTo := ""
	created := false
	deleted := false
	for _, entry := range args.Edits {
		if err := ctx.Err(); err != nil {
			return ToolResult{}, err
		}
		op := entry.Op
		if op == "" {
			op = "update"
		}
		switch op {
		case "create":
			if statErr == nil || created {
				return ToolResult{}, fmt.Errorf("tools: patch create target %q already exists", args.Path)
			}
			current = patchCreateContent(entry.Diff)
			created = true
			deleted = false
		case "delete":
			if statErr != nil && !created {
				return ToolResult{}, fmt.Errorf("tools: patch delete target %q: %w", args.Path, statErr)
			}
			current = ""
			deleted = true
		case "update":
			if deleted {
				return ToolResult{}, fmt.Errorf("tools: patch update follows delete for %q", args.Path)
			}
			if entry.Diff != "" {
				current, err = applyUnifiedEdit(current, entry.Diff)
				if err != nil {
					return ToolResult{}, fmt.Errorf("tools: patch %q: %w", args.Path, err)
				}
			}
		default:
			return ToolResult{}, fmt.Errorf("tools: unsupported patch operation %q", entry.Op)
		}
		if entry.Rename != "" {
			moveTo = entry.Rename
		}
	}
	if moveTo != "" && deleted {
		return ToolResult{}, fmt.Errorf("tools: deleted patch cannot be renamed")
	}
	if before.SHA256 != "" {
		if err := before.Verify(); err != nil {
			return ToolResult{}, err
		}
	}
	if moveTo != "" {
		target, resolveErr := workspace.Resolve(moveTo)
		if resolveErr != nil {
			return ToolResult{}, resolveErr
		}
		if _, statErr := os.Stat(target.Absolute); statErr == nil {
			return ToolResult{}, fmt.Errorf("tools: rename target %q already exists", moveTo)
		} else if !os.IsNotExist(statErr) {
			return ToolResult{}, statErr
		}
		if err := WriteFileAtomic(target.Absolute, []byte(current), fileMode(before)); err != nil {
			return ToolResult{}, err
		}
		if err := os.Remove(resolved.Absolute); err != nil {
			return ToolResult{}, fmt.Errorf("tools: patch rename %q: %w", args.Path, err)
		}
		return ToolResult{Content: fmt.Sprintf("patched %s and renamed to %s", resolved.Relative, target.Relative), Details: map[string]any{
			"path": resolved.Relative, "rename_to": target.Relative, "op": "patch",
		}}, nil
	}
	if deleted {
		if err := os.Remove(resolved.Absolute); err != nil {
			return ToolResult{}, fmt.Errorf("tools: patch delete %q: %w", args.Path, err)
		}
		return ToolResult{Content: fmt.Sprintf("patched %s (deleted)", resolved.Relative), Details: map[string]any{
			"path": resolved.Relative, "op": "patch", "deleted": true,
		}}, nil
	}
	if err := WriteFileAtomic(resolved.Absolute, []byte(current), fileMode(before)); err != nil {
		return ToolResult{}, err
	}
	after, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: fmt.Sprintf("patched %s", resolved.Relative), Details: map[string]any{
		"path": resolved.Relative, "op": "patch", "before_sha256": before.SHA256, "sha256": after.SHA256,
	}}, nil
}

func fileMode(snapshot FileSnapshot) os.FileMode {
	if snapshot.Mode != 0 {
		return snapshot.Mode
	}
	return 0o644
}

func patchCreateContent(diff string) string {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	prefixed := len(lines) > 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "+") {
			prefixed = false
			break
		}
	}
	if prefixed {
		for i := range lines {
			lines[i] = strings.TrimPrefix(lines[i], "+")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func applyUnifiedEdit(content, diff string) (string, error) {
	lines, trailing := editLines(content)
	hunks := parsePatchHunks(diff)
	if len(hunks) == 0 {
		return content, nil
	}
	for _, hunk := range hunks {
		old := make([]string, 0, len(hunk))
		updated := make([]string, 0, len(hunk))
		for _, line := range hunk {
			switch line.kind {
			case ' ', '-':
				old = append(old, line.text)
			}
			if line.kind != '-' {
				updated = append(updated, line.text)
			}
		}
		index, err := findPatchSequence(lines, old)
		if err != nil {
			return "", err
		}
		lines = append(append(append([]string{}, lines[:index]...), updated...), lines[index+len(old):]...)
	}
	return joinEditLines(lines, trailing), nil
}

func parsePatchHunks(diff string) [][]patchHunkLine {
	input := strings.ReplaceAll(diff, "\r\n", "\n")
	rows := strings.Split(input, "\n")
	var hunks [][]patchHunkLine
	var current []patchHunkLine
	flush := func() {
		if len(current) > 0 {
			hunks = append(hunks, current)
			current = nil
		}
	}
	for _, row := range rows {
		if strings.HasPrefix(row, "@@") {
			flush()
			continue
		}
		if row == "\\ No newline at end of file" || row == "…" {
			continue
		}
		if row == "" {
			continue
		}
		kind := row[0]
		if kind != ' ' && kind != '+' && kind != '-' {
			if len(hunks) == 0 && len(current) == 0 {
				current = append(current, patchHunkLine{kind: ' ', text: row})
			}
			continue
		}
		current = append(current, patchHunkLine{kind: kind, text: row[1:]})
	}
	flush()
	return hunks
}

func findPatchSequence(lines []string, pattern []string) (int, error) {
	if len(pattern) == 0 {
		return len(lines), nil
	}
	found := -1
	for i := 0; i+len(pattern) <= len(lines); i++ {
		match := true
		for j := range pattern {
			if lines[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			if found >= 0 {
				return 0, fmt.Errorf("patch context is ambiguous")
			}
			found = i
		}
	}
	if found < 0 {
		return 0, fmt.Errorf("patch context was not found")
	}
	return found, nil
}

type hashlineSection struct {
	path string
	tag  string
	ops  []hashlineOperation
}

type hashlineOperation struct {
	kind     string
	start    int
	end      int
	block    bool
	before   bool
	after    bool
	eof      bool
	register string
	payload  []string
	dest     string
}

type hashlineChange struct {
	start int
	end   int
	delta int
}

func (tool EditTool) applyHashline(ctx context.Context, execution ExecutionContext, args editArguments) (ToolResult, error) {
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	sections, err := parseHashlineSections(args.Input)
	if err != nil {
		return ToolResult{}, err
	}
	if len(sections) == 0 {
		return ToolResult{}, fmt.Errorf("tools: hashline input has no file sections")
	}
	results := make([]string, 0, len(sections))
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return ToolResult{}, err
		}
		resolved, resolveErr := workspace.Resolve(section.path)
		if resolveErr != nil {
			return ToolResult{}, resolveErr
		}
		before, snapshotErr := SnapshotFile(resolved.Absolute)
		if snapshotErr != nil {
			return ToolResult{}, snapshotErr
		}
		data, _, readErr := readTextFile(resolved.Absolute)
		if readErr != nil {
			return ToolResult{}, readErr
		}
		text := string(data)
		if !strings.EqualFold(section.tag, hashlineTag(text)) {
			return ToolResult{}, fmt.Errorf("tools: hashline tag for %q is stale; re-read the file", section.path)
		}
		updated, deleted, moveTo, applyErr := applyHashlineOperations(text, section.ops)
		if applyErr != nil {
			return ToolResult{}, fmt.Errorf("tools: hashline %q: %w", section.path, applyErr)
		}
		if deleted {
			if err := os.Remove(resolved.Absolute); err != nil {
				return ToolResult{}, err
			}
			results = append(results, fmt.Sprintf("deleted %s", resolved.Relative))
			continue
		}
		if updated == text && moveTo == "" {
			return ToolResult{}, fmt.Errorf("tools: hashline %q produced no change", section.path)
		}
		if err := before.Verify(); err != nil {
			return ToolResult{}, err
		}
		if moveTo != "" {
			target, resolveErr := workspace.Resolve(moveTo)
			if resolveErr != nil {
				return ToolResult{}, resolveErr
			}
			if _, statErr := os.Stat(target.Absolute); statErr == nil {
				return ToolResult{}, fmt.Errorf("tools: rename target %q already exists", moveTo)
			} else if !os.IsNotExist(statErr) {
				return ToolResult{}, statErr
			}
			if err := WriteFileAtomic(target.Absolute, []byte(updated), before.Mode); err != nil {
				return ToolResult{}, err
			}
			if err := os.Remove(resolved.Absolute); err != nil {
				return ToolResult{}, err
			}
			results = append(results, fmt.Sprintf("updated %s and renamed to %s", resolved.Relative, target.Relative))
			continue
		}
		if err := WriteFileAtomic(resolved.Absolute, []byte(updated), before.Mode); err != nil {
			return ToolResult{}, err
		}
		results = append(results, fmt.Sprintf("updated %s", resolved.Relative))
	}
	return ToolResult{Content: strings.Join(results, "\n"), Details: map[string]any{"op": "hashline", "files": len(results)}}, nil
}

func parseHashlineSections(input string) ([]hashlineSection, error) {
	rows := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	var sections []hashlineSection
	var current *hashlineSection
	for i := 0; i < len(rows); {
		row := strings.TrimSuffix(rows[i], "\r")
		if strings.TrimSpace(row) == "" || row == "*** Begin Patch" || row == "*** End Patch" {
			i++
			continue
		}
		if path, tag, ok := parseHashlineHeader(row); ok {
			if current != nil {
				sections = append(sections, *current)
			}
			current = &hashlineSection{path: path, tag: tag}
			i++
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("tools: hashline row %q appears before a file header", row)
		}
		op, hasBody, err := parseHashlineOperation(row)
		if err != nil {
			return nil, err
		}
		if !hasBody {
			current.ops = append(current.ops, op)
			i++
			continue
		}
		i++
		for i < len(rows) {
			body := rows[i]
			if _, _, ok := parseHashlineHeader(body); ok || isHashlineOperation(body) || body == "*** End Patch" {
				break
			}
			if strings.TrimSpace(body) == "" {
				i++
				continue
			}
			if !strings.HasPrefix(body, "+") {
				return nil, fmt.Errorf("tools: hashline payload must use + lines, got %q", body)
			}
			op.payload = append(op.payload, body[1:])
			i++
		}
		if len(op.payload) == 0 && op.register == "" {
			return nil, fmt.Errorf("tools: hashline PUT requires a payload")
		}
		current.ops = append(current.ops, op)
	}
	if current != nil {
		sections = append(sections, *current)
	}
	return sections, nil
}

func parseHashlineHeader(row string) (string, string, bool) {
	trimmed := strings.TrimSpace(row)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	at := strings.LastIndexByte(body, '#')
	if at <= 0 || len(body)-at-1 != 4 {
		return "", "", false
	}
	tag := body[at+1:]
	for _, char := range tag {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return "", "", false
		}
	}
	return strings.TrimSpace(body[:at]), tag, true
}

func isHashlineOperation(row string) bool {
	trimmed := strings.TrimSpace(row)
	return strings.HasPrefix(trimmed, "PUT ") || strings.HasPrefix(trimmed, "PUT:") || strings.HasPrefix(trimmed, "CUT ") || strings.HasPrefix(trimmed, "CUT:") || trimmed == "REM" || strings.HasPrefix(trimmed, "MV ")
}

func parseHashlineOperation(row string) (hashlineOperation, bool, error) {
	trimmed := strings.TrimSpace(row)
	if trimmed == "REM" {
		return hashlineOperation{kind: "remove"}, false, nil
	}
	if strings.HasPrefix(trimmed, "MV ") {
		dest := strings.TrimSpace(strings.TrimPrefix(trimmed, "MV "))
		if dest == "" {
			return hashlineOperation{}, false, fmt.Errorf("tools: hashline MV requires a destination")
		}
		return hashlineOperation{kind: "move", dest: dest}, false, nil
	}
	keyword := ""
	if strings.HasPrefix(trimmed, "PUT") {
		keyword = "PUT"
	} else if strings.HasPrefix(trimmed, "CUT") {
		keyword = "CUT"
	} else {
		return hashlineOperation{}, false, fmt.Errorf("tools: invalid hashline operation %q", row)
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, keyword))
	colon := strings.IndexByte(rest, ':')
	hasBody := keyword == "PUT" && colon >= 0
	targetText := rest
	if colon >= 0 {
		targetText = rest[:colon]
	}
	targetText = strings.TrimSpace(targetText)
	register := ""
	if at := strings.LastIndex(targetText, " @"); at >= 0 {
		register = strings.TrimSpace(targetText[at+2:])
		targetText = strings.TrimSpace(targetText[:at])
	}
	if register != "" && !validRegister(register) {
		return hashlineOperation{}, false, fmt.Errorf("tools: invalid hashline register %q", register)
	}
	op := hashlineOperation{kind: strings.ToLower(keyword), register: register}
	if targetText == ">$" {
		op.eof = true
		return op, hasBody, nil
	}
	if strings.HasPrefix(targetText, "<") || strings.HasPrefix(targetText, ">") {
		op.before = strings.HasPrefix(targetText, "<")
		op.after = strings.HasPrefix(targetText, ">")
		targetText = strings.TrimPrefix(strings.TrimPrefix(targetText, "<"), ">")
		if strings.HasSuffix(targetText, "*") {
			op.block = true
			targetText = strings.TrimSuffix(targetText, "*")
		}
		number, err := parsePositiveLine(targetText)
		if err != nil {
			return hashlineOperation{}, false, err
		}
		op.start, op.end = number, number
		return op, hasBody, nil
	}
	if strings.HasSuffix(targetText, "*") {
		op.block = true
		targetText = strings.TrimSuffix(targetText, "*")
	}
	parts := strings.Split(targetText, ".=")
	if len(parts) == 1 {
		number, err := parsePositiveLine(parts[0])
		if err != nil {
			return hashlineOperation{}, false, err
		}
		op.start, op.end = number, number
	} else if len(parts) == 2 {
		start, startErr := parsePositiveLine(parts[0])
		end, endErr := parsePositiveLine(parts[1])
		if startErr != nil || endErr != nil || end < start {
			return hashlineOperation{}, false, fmt.Errorf("tools: invalid hashline range %q", targetText)
		}
		op.start, op.end = start, end
	} else {
		return hashlineOperation{}, false, fmt.Errorf("tools: invalid hashline target %q", targetText)
	}
	return op, hasBody, nil
}

func validRegister(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char == '_' || char == '-' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
			return false
		}
	}
	return true
}

func parsePositiveLine(value string) (int, error) {
	var number int
	if value == "" {
		return 0, fmt.Errorf("tools: hashline line number is required")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("tools: invalid hashline line number %q", value)
		}
		number = number*10 + int(char-'0')
		if number < 1 {
			return 0, fmt.Errorf("tools: hashline line numbers start at 1")
		}
	}
	return number, nil
}

func applyHashlineOperations(text string, operations []hashlineOperation) (string, bool, string, error) {
	lines, trailing := editLines(text)
	original := append([]string(nil), lines...)
	changes := make([]hashlineChange, 0, len(operations))
	registers := make(map[string][]string)
	var anonymous []string
	moveTo := ""
	for _, op := range operations {
		if op.kind == "remove" {
			return "", true, "", nil
		}
		if op.kind == "move" {
			moveTo = op.dest
			continue
		}
		start, end := op.start-1, op.end
		if op.eof {
			start, end = len(original), len(original)
		} else if op.block {
			start, end = hashlineBlockRange(original, op.start)
		}
		if start < 0 || end < start || end > len(original) {
			return "", false, "", fmt.Errorf("hashline range %d..%d is outside the file", op.start, op.end)
		}
		currentStart := hashlineMappedIndex(start, changes)
		currentEnd := hashlineMappedIndex(end, changes)
		if currentStart < 0 || currentEnd < currentStart || currentEnd > len(lines) {
			return "", false, "", fmt.Errorf("hashline range %d..%d overlaps an earlier edit", op.start, op.end)
		}
		var payload []string
		if op.register != "" {
			payload = append([]string(nil), registers[op.register]...)
			if len(payload) == 0 {
				return "", false, "", fmt.Errorf("hashline register %q is empty", op.register)
			}
		} else if op.kind == "put" {
			payload = append([]string(nil), op.payload...)
		} else if op.kind == "cut" {
			payload = nil
		}
		if op.kind == "cut" && op.register != "" {
			registers[op.register] = append([]string(nil), lines[currentStart:currentEnd]...)
		}
		if op.kind == "cut" && op.register == "" {
			anonymous = append([]string(nil), lines[currentStart:currentEnd]...)
		}
		if op.kind == "put" && op.register == "" && len(payload) == 0 && len(anonymous) > 0 {
			payload = append([]string(nil), anonymous...)
		}
		if op.kind == "put" && op.before == false && op.after == false && op.eof == false {
			// range replacement
		} else if op.kind == "put" {
			if op.before {
				currentEnd = currentStart
			} else if op.after {
				currentStart = currentEnd
			} else if op.eof {
				currentStart, currentEnd = len(lines), len(lines)
			}
		}
		lines = append(append(append([]string{}, lines[:currentStart]...), payload...), lines[currentEnd:]...)
		changes = append(changes, hashlineChange{start: start, end: end, delta: len(payload) - (currentEnd - currentStart)})
	}
	return joinEditLines(lines, trailing), false, moveTo, nil
}

func hashlineMappedIndex(position int, changes []hashlineChange) int {
	mapped := position
	for _, change := range changes {
		if change.end <= position {
			mapped += change.delta
		}
	}
	return mapped
}

func hashlineBlockRange(lines []string, line int) (int, int) {
	start := line - 1
	if start < 0 || start >= len(lines) {
		return start, start + 1
	}
	trimmed := strings.TrimSpace(lines[start])
	if strings.HasPrefix(trimmed, "#") {
		level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		end := start + 1
		for end < len(lines) {
			next := strings.TrimSpace(lines[end])
			nextLevel := len(next) - len(strings.TrimLeft(next, "#"))
			if strings.HasPrefix(next, "#") && nextLevel <= level {
				break
			}
			end++
		}
		return start, end
	}
	if strings.Contains(lines[start], "{") {
		depth := 0
		for end := start; end < len(lines); end++ {
			depth += strings.Count(lines[end], "{") - strings.Count(lines[end], "}")
			if end > start && depth <= 0 {
				return start, end + 1
			}
		}
		return start, len(lines)
	}
	indent := len(lines[start]) - len(strings.TrimLeft(lines[start], " \t"))
	end := start + 1
	for end < len(lines) {
		if strings.TrimSpace(lines[end]) != "" {
			nextIndent := len(lines[end]) - len(strings.TrimLeft(lines[end], " \t"))
			if nextIndent <= indent {
				break
			}
		}
		end++
	}
	return start, end
}

func editLines(text string) ([]string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	trailing := strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, trailing
	}
	return strings.Split(text, "\n"), trailing
}

func joinEditLines(lines []string, trailing bool) string {
	text := strings.Join(lines, "\n")
	if trailing && len(lines) > 0 {
		text += "\n"
	}
	return text
}

func hashlineTag(text string) string {
	normalized := make([]byte, 0, len(text))
	for _, segment := range strings.SplitAfter(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSuffix(segment, "\n")
		line = strings.TrimRight(line, " \t\r")
		normalized = append(normalized, line...)
		if strings.HasSuffix(segment, "\n") {
			normalized = append(normalized, '\n')
		}
	}
	return fmt.Sprintf("%04X", xxhash32(normalized)&0xffff)
}

func xxhash32(input []byte) uint32 {
	const (
		p1 uint32 = 2654435761
		p2 uint32 = 2246822519
		p3 uint32 = 3266489917
		p4 uint32 = 668265263
		p5 uint32 = 374761393
	)
	rotl := func(value uint32, count uint) uint32 { return value<<count | value>>(32-count) }
	read := func(offset int) uint32 { return binary.LittleEndian.Uint32(input[offset : offset+4]) }
	length := len(input)
	index := 0
	var hash uint32
	if length >= 16 {
		v1, v2, v3, v4 := p1, p2, uint32(0), uint32(0)
		v1 += p2
		v3 -= p1
		v4 -= p2
		for index <= length-16 {
			v1 = rotl(v1+read(index)*p2, 13) * p1
			index += 4
			v2 = rotl(v2+read(index)*p2, 13) * p1
			index += 4
			v3 = rotl(v3+read(index)*p2, 13) * p1
			index += 4
			v4 = rotl(v4+read(index)*p2, 13) * p1
			index += 4
		}
		hash = rotl(v1, 1) + rotl(v2, 7) + rotl(v3, 12) + rotl(v4, 18)
	} else {
		hash = p5
	}
	hash += uint32(length)
	for index <= length-4 {
		hash = rotl(hash+read(index)*p3, 17) * p4
		index += 4
	}
	for index < length {
		hash = rotl(hash+uint32(input[index])*p5, 11) * p1
		index++
	}
	hash ^= hash >> 15
	hash *= p2
	hash ^= hash >> 13
	hash *= p3
	hash ^= hash >> 16
	return hash
}
