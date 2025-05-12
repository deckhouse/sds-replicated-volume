package drbdconf

type Root struct {
	Filename string
	Elements []RootElement
}

// [Section] or [Include]
type RootElement interface {
	_configElement()
}

type Include struct {
	Glob    string
	Configs []*Root
}

func (*Include) _configElement() {}

type Section struct {
	Key      []Word
	Elements []SectionElement
}

// [Section] or [Parameter]
type SectionElement interface {
	_sectionElement()
}

func (*Section) _configElement()  {}
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
