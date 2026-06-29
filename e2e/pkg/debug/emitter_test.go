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
	"bytes"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WriterEmitter", func() {
	var (
		buf bytes.Buffer
		em  *WriterEmitter
	)

	Context("single writer", func() {
		BeforeEach(func() {
			buf.Reset()
			em = NewWriterEmitter(&buf, nil)
		})

		It("Emit writes line with newline", func() {
			em.Emit("hello world")
			Expect(buf.String()).To(Equal("hello world\n"))
		})

		It("EmitBlock writes all lines", func() {
			em.EmitBlock([]string{"line 1", "line 2", "line 3"})
			Expect(buf.String()).To(Equal("line 1\nline 2\nline 3\n"))
		})

		It("EmitBlock with nil writes nothing", func() {
			em.EmitBlock(nil)
			Expect(buf.Len()).To(BeZero())
		})
	})

	Context("dual writer", func() {
		It("preserves ANSI in primary, strips in plain", func() {
			var primary, plain bytes.Buffer
			em = NewWriterEmitter(&primary, &plain)

			colored := "\033[31mred text\033[0m"
			em.Emit(colored)

			Expect(primary.String()).To(ContainSubstring("\033[31m"))
			Expect(plain.String()).NotTo(ContainSubstring("\033["))
			Expect(plain.String()).To(ContainSubstring("red text"))
		})

		It("EmitBlock strips ANSI in plain", func() {
			var primary, plain bytes.Buffer
			em = NewWriterEmitter(&primary, &plain)

			lines := []string{
				"\033[32mgreen\033[0m",
				"\033[1;31mbold red\033[0m",
			}
			em.EmitBlock(lines)

			Expect(plain.String()).NotTo(ContainSubstring("\033["))
			Expect(plain.String()).To(ContainSubstring("green"))
			Expect(plain.String()).To(ContainSubstring("bold red"))
		})
	})

	Context("concurrent safety", func() {
		It("EmitBlock from 10 goroutines remain contiguous", func() {
			var concBuf bytes.Buffer
			concEm := NewWriterEmitter(&concBuf, nil)

			const goroutines = 10
			const linesPerGoroutine = 5

			var wg sync.WaitGroup
			wg.Add(goroutines)
			for g := 0; g < goroutines; g++ {
				go func(id int) {
					defer wg.Done()
					lines := make([]string, linesPerGoroutine)
					for l := 0; l < linesPerGoroutine; l++ {
						lines[l] = fmt.Sprintf("goroutine-%d-line-%d", id, l)
					}
					concEm.EmitBlock(lines)
				}(g)
			}
			wg.Wait()

			output := concBuf.String()
			outputLines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")

			for g := 0; g < goroutines; g++ {
				for l := 0; l < linesPerGoroutine; l++ {
					needle := fmt.Sprintf("goroutine-%d-line-%d", g, l)
					Expect(output).To(ContainSubstring(needle))
				}
			}

			for g := 0; g < goroutines; g++ {
				first := -1
				for i, line := range outputLines {
					if line == fmt.Sprintf("goroutine-%d-line-0", g) {
						first = i
						break
					}
				}
				Expect(first).NotTo(Equal(-1), "goroutine %d: first line not found", g)
				for l := 0; l < linesPerGoroutine; l++ {
					want := fmt.Sprintf("goroutine-%d-line-%d", g, l)
					idx := first + l
					Expect(idx).To(BeNumerically("<", len(outputLines)),
						"goroutine %d: expected line at index %d but output has only %d lines", g, idx, len(outputLines))
					Expect(outputLines[idx]).To(Equal(want),
						"goroutine %d: line %d not contiguous", g, l)
				}
			}
		})
	})
})

var _ = Describe("AnsiRe", func() {
	DescribeTable("strips codes",
		func(input, want string) {
			got := AnsiRe.ReplaceAllString(input, "")
			Expect(got).To(Equal(want))
		},
		Entry("simple red", "\033[31mred\033[0m", "red"),
		Entry("bold red", "\033[1;31mbold red\033[0m", "bold red"),
		Entry("dim with trailing text", "\033[2mdim\033[0m text", "dim text"),
		Entry("no colors", "no colors here", "no colors here"),
		Entry("only codes", "\033[31m\033[32m\033[0m", ""),
	)
})

var _ = Describe("ColorsEnabled", func() {
	It("consistent with flag", func() {
		if ColorsEnabled {
			Expect(ColorRed).NotTo(BeEmpty())
			Expect(ColorReset).NotTo(BeEmpty())
		} else {
			Expect(ColorRed).To(BeEmpty())
			Expect(ColorReset).To(BeEmpty())
		}
	})
})

var _ = Describe("EmitBlock empty slice", func() {
	It("empty slice writes nothing", func() {
		var buf bytes.Buffer
		em := NewWriterEmitter(&buf, nil)
		em.EmitBlock([]string{})
		Expect(buf.Len()).To(BeZero())
	})
})
