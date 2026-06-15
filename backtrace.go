package errorgap

import (
	"runtime"
	"strings"
)

// Frame is a single backtrace entry in the notice envelope.
type Frame struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
	InApp    bool   `json:"in_app"`
	Index    int    `json:"index"`
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
			})
			index++
		}
		if !more {
			break
		}
	}
	return out
}

func relativeFile(file, root string) string {
	if root == "" {
		return file
	}
	normalized := root
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
	if strings.Contains(file, "/vendor/") {
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
