package errorgap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SourceExcerpt contains source lines surrounding a backtrace frame.
type SourceExcerpt struct {
	StartLine int      `json:"start_line"`
	Lines     []string `json:"lines"`
}

// Frame is a single backtrace entry in the notice envelope.
type Frame struct {
	File     string         `json:"file,omitempty"`
	Line     int            `json:"line,omitempty"`
	Function string         `json:"function,omitempty"`
	InApp    bool           `json:"in_app"`
	Index    int            `json:"index"`
	Source   *SourceExcerpt `json:"source,omitempty"`
}

// captureBacktrace walks the current goroutine's stack, starting `skip`
// frames in from the caller, and returns a normalized Frame slice.
func captureBacktrace(skip int, rootDirectory string) []Frame {
	const maxDepth = 64
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(skip+1, pcs)
	if n == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:n])
	var out []Frame
	index := 0
	for {
		f, more := frames.Next()
		if f.File != "" {
			out = append(out, Frame{
				File:     relativeFile(f.File, rootDirectory),
				Line:     f.Line,
				Function: f.Function,
				InApp:    isInApp(f.File, rootDirectory),
				Index:    index,
				Source:   readSourceExcerpt(f.File, f.Line),
			})
			index++
		}
		if !more {
			break
		}
	}
	return out
}

func readSourceExcerpt(file string, line int) *SourceExcerpt {
	if file == "" || line <= 0 {
		return nil
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
	if line > len(lines) {
		return nil
	}
	start := line - 3
	if start < 1 {
		start = 1
	}
	end := line + 3
	if end > len(lines) {
		end = len(lines)
	}
	return &SourceExcerpt{StartLine: start, Lines: append([]string(nil), lines[start-1:end]...)}
}

func relativeFile(file, root string) string {
	if root == "" {
		return filepath.ToSlash(file)
	}
	normalized := filepath.ToSlash(filepath.Clean(root))
	file = filepath.ToSlash(filepath.Clean(file))
	if !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	if strings.HasPrefix(file, normalized) {
		return strings.TrimPrefix(file, normalized)
	}
	return file
}

func isInApp(file, root string) bool {
	if file == "" {
		return false
	}
	file = filepath.ToSlash(filepath.Clean(file))
	root = filepath.ToSlash(filepath.Clean(root))
	if strings.Contains(file, "/vendor/") {
		return false
	}
	if strings.Contains(file, "/third_party/") {
		return false
	}
	if strings.Contains(file, "go/pkg/mod") {
		return false
	}
	if strings.HasPrefix(file, "/usr/") || strings.HasPrefix(file, "/opt/") {
		return false
	}
	if root == "" {
		return false
	}
	return strings.HasPrefix(file, root)
}
