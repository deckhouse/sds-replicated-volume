package v9

import "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"

type Section interface {
	Keyword() string
	SectionReader
}

type SectionReader interface {
	Read(sec *drbdconf.Section) error
}

// useful type constraint
type SectionPtr[T any] interface {
	*T
	Section
}
