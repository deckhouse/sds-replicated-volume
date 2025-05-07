package v9

import (
	"os"
	"testing"
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

	for res := range config.Resources() {
		res.Options().SetQuorumMinimumRedundancy(2)
	}
}
