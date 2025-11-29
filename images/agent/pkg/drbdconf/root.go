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
	"iter"
)

type Root struct {
	Filename string
	Elements []RootElement
}

func (root *Root) AsSection() *Section {
	sec := &Section{}

	for _, subSec := range root.TopLevelSections() {
		sec.Elements = append(sec.Elements, subSec)
	}

	return sec
}

func (root *Root) TopLevelSections() iter.Seq2[*Root, *Section] {
	return func(yield func(*Root, *Section) bool) {
		visited := map[*Root]struct{}{root: {}}

		for _, el := range root.Elements {
			if sec, ok := el.(*Section); ok {
				if !yield(root, sec) {
					return
				}
				continue
			}
			incl := el.(*Include)
			for _, subRoot := range incl.Files {
				if _, ok := visited[subRoot]; ok {
					continue
				}
				visited[subRoot] = struct{}{}

				for secRoot, sec := range subRoot.TopLevelSections() {
					if !yield(secRoot, sec) {
						return
					}
				}
			}
		}
	}
}

// [Section] or [Include]
type RootElement interface {
	_rootElement()
}

type Include struct {
	Glob  string
	Files []*Root
}

func (*Include) _rootElement() {}

type Section struct {
	Key      []Word
	Elements []SectionElement
}

// [Section] or [Parameter]
type SectionElement interface {
	_sectionElement()
}

func (*Section) _rootElement()    {}
func (*Section) _sectionElement() {}

func (s *Section) Location() Location { return s.Key[0].Location }

func (s *Section) ParametersByKey(name string) iter.Seq[*Parameter] {
	return func(yield func(*Parameter) bool) {
		for par := range s.Parameters() {
			if par.Key[0].Value == name {
				if !yield(par) {
					return
				}
			}
		}
	}
}

func (s *Section) Parameters() iter.Seq[*Parameter] {
	return func(yield func(*Parameter) bool) {
		for _, el := range s.Elements {
			if par, ok := el.(*Parameter); ok {
				if !yield(par) {
					return
				}
			}
		}
	}
}

func (s *Section) SectionsByKey(name string) iter.Seq[*Section] {
	return func(yield func(*Section) bool) {
		for par := range s.Sections() {
			if par.Key[0].Value == name {
				if !yield(par) {
					return
				}
			}
		}
	}
}

func (s *Section) Sections() iter.Seq[*Section] {
	return func(yield func(*Section) bool) {
		for _, el := range s.Elements {
			if par, ok := el.(*Section); ok {
				if !yield(par) {
					return
				}
			}
		}
	}
}

type Parameter struct {
	Key []Word
}

func (*Parameter) _sectionElement()     {}
func (p *Parameter) Location() Location { return p.Key[0].Location }

type Word struct {
	// means that token is definetely not a keyword, but a value
	IsQuoted bool
	// Unquoted value
	Value    string
	Location Location
}

func NewWord(word string) Word {
	return Word{
		Value:    word,
		IsQuoted: len(word) == 0 || !isTokenStr(word),
	}
}

func NewWords(wordStrs []string) []Word {
	words := make([]Word, len(wordStrs))
	for i, s := range wordStrs {
		words[i] = NewWord(s)
	}
	return words
}

func (*Word) _token() {}

func (w *Word) LocationEnd() Location {
	loc := w.Location
	loc.ColIndex += len(w.Value)
	return loc
}

type Location struct {
	// for error reporting only, zero-based
	LineIndex, ColIndex int
	Filename            string
}

func (l Location) NextLine() Location {
	return Location{l.LineIndex + 1, 0, l.Filename}
}

func (l Location) NextCol() Location {
	return Location{l.LineIndex, l.ColIndex + 1, l.Filename}
}

func (l Location) String() string {
	return fmt.Sprintf("%s [Ln %d, Col %d]", l.Filename, l.LineIndex+1, l.ColIndex+1)
}
