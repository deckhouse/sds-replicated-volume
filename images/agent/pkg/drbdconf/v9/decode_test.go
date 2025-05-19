package v9

import (
	"os"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

func TestUnmarshal(t *testing.T) {
	fsRoot, err := os.OpenRoot("./../testdata/")
	if err != nil {
		t.Fatal(err)
	}

	root, err := drbdconf.Parse(fsRoot.FS(), "root.conf")
	if err != nil {
		t.Fatal(err)
	}

	v9Conf := &Config{}

	if err := drbdconf.Unmarshal(root.AsSection(), v9Conf); err != nil {
		t.Fatal(err)
	}

}
