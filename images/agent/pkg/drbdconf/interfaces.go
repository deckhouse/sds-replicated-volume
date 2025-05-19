package drbdconf

type SectionKeyworder interface {
	SectionKeyword() string
}

type ParameterCodec interface {
	ParameterMarshaler
	ParameterUnmarshaler
}

type ParameterMarshaler interface {
	MarshalParameter() ([]string, error)
}

type ParameterUnmarshaler interface {
	UnmarshalParameter(p []Word) error
}

// # Type constraints

type SectionPtr[T any] interface {
	*T
	SectionKeyworder
}

type Ptr[T any] interface {
	*T
}
