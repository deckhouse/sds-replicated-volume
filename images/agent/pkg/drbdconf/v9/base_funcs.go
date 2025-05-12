package v9

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

// func (c *Common) Read(sec *drbdconf.Section) (bool, error) {

type SectionReader interface {
	Read(sec *drbdconf.Section) (bool, error)
}

// TODO
func readSingleSection[T SectionReader](root *drbdconf.Root) {
	// TODO: validate
	// T is struct, e.g.: Common
	// *T is SectionReader

	var common *T
	var commonRoot *drbdconf.Root   // for error text only
	var commonSec *drbdconf.Section // for error text only

	for root, sec := range root.TopLevelSections() {
		var commonTmp T
		x, ok := commonTmp.(SectionReader)

		if ok, err := commonTmp.(SectionReader).Read(sec); ok && common != nil {
			return nil,
				fmt.Errorf(
					"duplicate section 'common': '%s' %s, '%s' %s",
					commonRoot.Filename, commonSec.Location,
					root.Filename, sec.Location,
				)
		} else if err != nil {
			// validation error
			return nil,
				fmt.Errorf(
					"invalid section 'common' at '%s' %s: %w",
					root.Filename, sec.Location, err,
				)
		} else if ok {
			// success
			common = commonTmp
			commonRoot = root
			commonSec = sec
		}
	}
}
