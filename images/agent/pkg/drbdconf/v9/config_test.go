package v9

import (
	"strings"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

func TestV9Config(t *testing.T) {
	// root, err := os.OpenRoot("./testdata/")
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// config, err := OpenConfig(root.FS(), "root.conf")
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// for res := range config.Resources {
	// 	_ = res
	// 	// res.Options.SetQuorumMinimumRedundancy(2)
	// }
}

func TestMarshal(t *testing.T) {
	cfg := &Config{
		Global: &Global{
			DialogRefresh:         ptr(42),
			DisableIPVerification: true,
			UsageCount:            UsageCountValueAsk,
			UdevAlwaysUseVNR:      true,
		},
		Common: &Common{
			Disk: &DiskOptions{
				ALExtents:     ptr(uint(123)),
				ALUpdates:     ptr(false),
				DiskDrain:     ptr(true),
				OnIOError:     IOErrorPolicyDetach,
				ReadBalancing: ReadBalancingPolicy64KStriping,
				ResyncAfter:   "asd/asd",
			},
		},
	}

	rootSec := &drbdconf.Section{}

	err := drbdconf.Marshal(cfg, rootSec)
	if err != nil {
		t.Fatal(err)
	}

	root := &drbdconf.Root{}
	for _, sec := range rootSec.Elements {
		root.Elements = append(root.Elements, sec.(*drbdconf.Section))
	}

	sb := &strings.Builder{}

	_, err = root.WriteTo(sb)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("\n", sb.String())
}
