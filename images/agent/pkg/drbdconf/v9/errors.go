package v9

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

func errDuplicateSection[T any, TP SectionPtr[T]](loc drbdconf.Location) error {
	return fmt.Errorf("duplicate section '%s': %s", TP(nil).Keyword(), loc)
}
