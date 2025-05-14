package v9

import (
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

type Section interface {
	Keyworder
	SectionUnmarshaler
	SectionMarshaller
}

type Keyworder interface {
	Keyword() string
}

type SectionMarshaller interface {
	MarshalToSection() *drbdconf.Section
}

type SectionUnmarshaler interface {
	UnmarshalFromSection(sec *drbdconf.Section) error
}

// # Type constraints

type SectionPtr[T any] interface {
	*T
	Section
}

type KeyworderPtr[T any] interface {
	*T
	Keyworder
}
