package drbdconf

type Config struct {
	Filename string
	Elements []ConfigElement
}

// [Section] or [Include]
type ConfigElement interface {
	isConfigElement()
}

type Include struct {
	ConfigElement
	Glob    string
	Configs []*Config
}

func (*Include) isConfigElement() {}

type Section struct {
	ConfigElement
	Key      []Word
	Elements []SectionElement
}

// [Section] or [Parameter]
type SectionElement interface {
	isSectionElement()
}

func (*Section) isConfigElement()  {}
func (*Section) isSectionElement() {}

type Parameter struct {
	Key []Word
}

func (*Parameter) isSectionElement() {}

type Word struct {
	// means that token is definetely not a keyword, but a value
	IsQuoted bool
	// Unquoted value
	Value string
}

func (*Word) isToken() {}
