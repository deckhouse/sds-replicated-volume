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

	// TODO validate

	_ = root

	return &Config{}, nil
}
