package v9

import (
	"fmt"
	"io/fs"
	"iter"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

type Config struct {
	store *drbdconf.Config
}

func OpenConfig(f fs.FS, name string) (*Config, error) {
	root, err := drbdconf.Parse(f, name)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// TODO validate

	return &Config{
		store: root,
	}, nil
}

func (c *Config) Common() *Common {
	panic("todo")
}

func (c *Config) Global() *Global {
	panic("todo")
}

func (c *Config) Resources() iter.Seq[*Resource] {
	panic("todo")
}

type Common struct {
}

func (c *Common) Disk() *Disk {
	panic("todo")
}

func (c *Common) Handlers() *Handlers {
	panic("todo")
}

type Global struct {
}

type Resource struct {
}

func (r *Resource) Options() *Options {
	panic("todo")
}

type Net struct {
}

type Disk struct {
	ResyncRate string
}

type Handlers struct {
}

type Options struct {
}

func (o *Options) SetQuorumMinimumRedundancy(val int) {
	// quorum-minimum-redundancy
	panic("todo")
}

type Startup struct {
}
