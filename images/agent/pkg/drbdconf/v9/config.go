// Missing resources:
//   - require-drbd-module-version-{eq,ne,gt,ge,lt,le}
//   - stacked-on-top-of
//
// Missing resource parameters:
//   - net.transport
package v9

import (
	"fmt"
	"io/fs"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

type Config struct {
	Common    *Common
	Global    *Global
	Resources []*Resource
}

func OpenConfig(f fs.FS, name string) (*Config, error) {
	root, err := drbdconf.Parse(f, name)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// validate

	var common *Common
	var commonRoot *drbdconf.Root   // for error text only
	var commonSec *drbdconf.Section // for error text only

	var global *Global
	var resources []*Resource

	for secRoot, sec := range root.TopLevelSections() {
		commonTmp := &Common{}
		if ok, err := commonTmp.Read(sec); ok && common != nil {
			return nil,
				fmt.Errorf(
					"duplicate section 'common': '%s' %s, '%s' %s",
					commonRoot.Filename, commonSec.Location,
					secRoot.Filename, sec.Location,
				)
		} else if err != nil {
			// validation error
			return nil,
				fmt.Errorf(
					"invalid section 'common' at '%s' %s: %w",
					secRoot.Filename, sec.Location, err,
				)
		} else if ok {
			// success
			common = commonTmp
			commonRoot = secRoot
			commonSec = sec
		}
	}

	return &Config{Common: common, Global: global, Resources: resources}, nil
}
