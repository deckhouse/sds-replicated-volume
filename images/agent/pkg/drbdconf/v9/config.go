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

	var global *Global
	var common *Common

	var resources []*Resource
	resourceNames := map[string]struct{}{}

	for _, sec := range root.TopLevelSections() {
		switch sec.Key[0].Value {
		case Keyword[Global]():
			if global != nil {
				return nil, errDuplicateSection[Global](sec.Location())
			}
			if err = global.UnmarshalFromSection(sec); err != nil {
				return nil, err
			}
		case Keyword[Common]():
			if common != nil {
				return nil, errDuplicateSection[Common](sec.Location())
			}
			if err = common.UnmarshalFromSection(sec); err != nil {
				return nil, err
			}
		case Keyword[Resource]():
			r := new(Resource)
			if err = r.UnmarshalFromSection(sec); err != nil {
				return nil, err
			}
			if _, ok := resourceNames[r.Name]; ok {
				return nil, fmt.Errorf(
					"%s: duplicate resource name: '%s'",
					sec.Location(),
					r.Name,
				)
			}
			resourceNames[r.Name] = struct{}{}
			resources = append(resources, r)
		}
	}

	return &Config{Common: common, Global: global, Resources: resources}, nil
}
