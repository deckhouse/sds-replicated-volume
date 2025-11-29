/*
Copyright 2025 Flant JSC

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

package drbdconf

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

var _ io.WriterTo = &Root{}

func (r *Root) WalkConfigs(accept func(conf *Root) error) error {
	for _, el := range r.Elements {
		if incl, ok := el.(*Include); ok {
			for _, childConf := range incl.Files {
				if err := childConf.WalkConfigs(accept); err != nil {
					return fmt.Errorf("callback error: %w", err)
				}
			}
		}
	}
	if err := accept(r); err != nil {
		return fmt.Errorf("callback error: %w", err)
	}
	return nil
}

func (r *Root) WriteTo(w io.Writer) (n int64, err error) {
	// TODO streaming
	sb := &strings.Builder{}

	for _, el := range r.Elements {
		switch tEl := el.(type) {
		case *Include:
			sb.WriteString("include ")
			sb.WriteString(strconv.Quote(tEl.Glob))
			sb.WriteString(";\n")
		case *Section:
			writeSectionTo(tEl, sb, "")
		}
		sb.WriteString("\n")
	}

	return io.Copy(w, strings.NewReader(sb.String()))
}

func writeSectionTo(s *Section, sb *strings.Builder, indent string) {
	writeWordsTo(s.Key, sb, indent)
	sb.WriteString(" {\n")

	nextIndent := indent + "\t"
	for _, el := range s.Elements {
		switch tEl := el.(type) {
		case (*Section):
			writeSectionTo(tEl, sb, nextIndent)
		case (*Parameter):
			writeWordsTo(tEl.Key, sb, nextIndent)
			sb.WriteString(";\n")
		default:
			panic("unknown section element type")
		}
	}

	sb.WriteString(indent)
	sb.WriteString("}\n")
}

func writeWordsTo(words []Word, sb *strings.Builder, indent string) {
	sb.WriteString(indent)
	for i, word := range words {
		if i > 0 {
			sb.WriteString(" ")
		}
		if word.IsQuoted {
			sb.WriteString(strconv.Quote(word.Value))
		} else {
			sb.WriteString(word.Value)
		}
	}
}
