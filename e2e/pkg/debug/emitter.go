/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package debug

import (
	"fmt"
	"io"
	"sync"
)

// Emitter is the interface for outputting formatted lines. Implementations
// must be safe for concurrent use.
type Emitter interface {
	// Emit writes a single line to the output.
	Emit(line string)
	// EmitBlock writes multiple lines atomically — no interleaving with
	// output from other goroutines.
	EmitBlock(lines []string)
}

// WriterEmitter writes colored output to a primary writer (e.g. GinkgoWriter
// or os.Stdout) and optionally strips ANSI codes for a secondary plain-text
// writer (e.g. a log file).
type WriterEmitter struct {
	mu      sync.Mutex
	primary io.Writer
	plain   io.Writer // optional; nil means no plain-text copy
}

// NewWriterEmitter returns an Emitter that writes to w. If plain is non-nil,
// a copy of every line with ANSI codes stripped is also written there.
func NewWriterEmitter(w io.Writer, plain io.Writer) *WriterEmitter {
	return &WriterEmitter{primary: w, plain: plain}
}

// Emit writes a single line followed by a newline.
func (e *WriterEmitter) Emit(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writeLocked(line)
}

// EmitBlock writes multiple lines atomically under a single lock.
func (e *WriterEmitter) EmitBlock(lines []string) {
	if len(lines) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, line := range lines {
		e.writeLocked(line)
	}
}

func (e *WriterEmitter) writeLocked(line string) {
	_, _ = fmt.Fprintln(e.primary, line) // best-effort write
	if e.plain != nil {
		clean := AnsiRe.ReplaceAllString(line, "")
		_, _ = fmt.Fprintln(e.plain, clean) // best-effort write
	}
}
