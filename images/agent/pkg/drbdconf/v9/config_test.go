package v9

import (
	"os"
	"strings"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

func TestV9Config(t *testing.T) {
	root, err := os.OpenRoot("./testdata/")
	if err != nil {
		t.Fatal(err)
	}

	config, err := OpenConfig(root.FS(), "root.conf")
	if err != nil {
		t.Fatal(err)
	}

	for res := range config.Resources {
		_ = res
		// res.Options.SetQuorumMinimumRedundancy(2)
	}
}

func TestMarshal(t *testing.T) {
	cfg := &Config{
		Global: &Global{
			DialogRefresh:         &[]int{42}[0],
			DisableIPVerification: true,
		},
	}

	sections, err := drbdconf.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	root := &drbdconf.Root{}
	for _, sec := range sections {
		root.Elements = append(root.Elements, sec)
	}

	sb := &strings.Builder{}

	_, err = root.WriteTo(sb)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("\n", sb.String())
}
