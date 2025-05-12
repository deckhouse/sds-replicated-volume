package drbdconf

type Root struct {
	Filename string
	Elements []RootElement
}

// [Section] or [Include]
type RootElement interface {
	_rootElement()
}

type Include struct {
	Glob    string
	Configs []*Root
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

type Parameter struct {
	Key []Word
}

func (*Parameter) _sectionElement() {}

type Word struct {
	// means that token is definetely not a keyword, but a value
	IsQuoted bool
	// Unquoted value
	Value string
}

func (*Word) _token() {}
