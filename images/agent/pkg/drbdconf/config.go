package drbdconf

import (
	"fmt"
	"iter"
)

type Root struct {
	Filename string
	Elements []RootElement
}

func (root *Root) TopLevelSections() iter.Seq2[*Root, *Section] {
	return func(yield func(*Root, *Section) bool) {
		visited := map[*Root]struct{}{root: {}}

		for _, el := range root.Elements {
			if sec, ok := el.(*Section); ok {
				if !yield(root, sec) {
					return
				}
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
	Location Location
}

// [Section] or [Parameter]
type SectionElement interface {
	_sectionElement()
}

func (*Section) _rootElement()    {}
func (*Section) _sectionElement() {}

type Parameter struct {
	Key      []Word
	Location Location
}

func (*Parameter) _sectionElement() {}

type Word struct {
	// means that token is definetely not a keyword, but a value
	IsQuoted bool
	// Unquoted value
	Value    string
	Location Location
}

func (*Word) _token() {}

type Location struct {
	// for error reporting only, zero-based
	LineIndex, ColIndex int
}

func (l Location) NextLine() Location {
	return Location{l.LineIndex + 1, 0}
}

func (l Location) NextCol() Location {
	return Location{l.LineIndex, l.ColIndex + 1}
}

func (l Location) String() string {
	return fmt.Sprintf("[Ln %d, Col %d]", l.LineIndex+1, l.ColIndex+1)
}
