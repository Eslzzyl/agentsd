package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type lineSelector struct {
	Raw       bool
	StartLine int
	EndLine   int
}

func decodeJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

var lineRangePattern = regexp.MustCompile(`^(.*):(\d+)(?:-(\d+)|\+(\d+))?$`)

func splitLineSelector(input string) (string, lineSelector, error) {
	if strings.HasSuffix(strings.ToLower(input), ":raw") {
		path := input[:len(input)-4]
		if path == "" {
			return "", lineSelector{}, fmt.Errorf("tools: selector path is empty")
		}
		return path, lineSelector{Raw: true}, nil
	}
	match := lineRangePattern.FindStringSubmatch(input)
	if match == nil || filepath.VolumeName(match[1]) == match[1] {
		return input, lineSelector{}, nil
	}
	start, _ := strconv.Atoi(match[2])
	end := start
	if match[3] != "" {
		end, _ = strconv.Atoi(match[3])
	} else if match[4] != "" {
		end = start + atoi(match[4]) - 1
	}
	if start < 1 || end < start {
		return "", lineSelector{}, fmt.Errorf("tools: invalid line selector %q", input)
	}
	return match[1], lineSelector{StartLine: start, EndLine: end}, nil
}

func atoi(value string) int {
	result, _ := strconv.Atoi(value)
	return result
}

func resolveToolWorkspace(configured Workspace, session SessionContext) (Workspace, error) {
	if configured.Root != "" {
		return configured, nil
	}
	root := session.WorkspaceRoot
	if root == "" {
		root = session.CWD
	}
	return NewWorkspace(root)
}

func readTextFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, info, fmt.Errorf("tools: %q is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, info, err
	}
	if isBinary(data) {
		return nil, info, fmt.Errorf("tools: %q appears to be binary", path)
	}
	return data, info, nil
}

func isBinary(data []byte) bool {
	for _, value := range data {
		if value == 0 {
			return true
		}
	}
	return false
}

func relativeDisplay(workspace Workspace, path string) string {
	relative, err := filepath.Rel(workspace.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
