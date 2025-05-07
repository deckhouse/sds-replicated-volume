package drbdconf

import (
	"fmt"
	"os"
	"testing"
)

func TestConf(t *testing.T) {
	root, err := os.OpenRoot("./testdata/")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse(root.FS(), "root.conf")
	if err != nil {
		t.Fatal(err)
	}

	err = cfg.WalkConfigs(func(conf *Config) error {
		filename := "./testdata/out/" + conf.Filename
		file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("open file %s: %w", filename, err)
		}
		if n, err := conf.WriteTo(file); err != nil {
			return fmt.Errorf("writing to file %s: %w", filename, err)
		} else {
			t.Logf("wrote %d bytes to %s", n, filename)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = cfg
}
