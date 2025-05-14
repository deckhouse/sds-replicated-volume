package v9

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

func readValueFromWord[T any, PT *T](
	val *PT,
	words []drbdconf.Word,
	wordIdx int,
	ctor func(string) (T, error),
	loc drbdconf.Location,
) error {
	if len(words) <= wordIdx {
		return fmt.Errorf("missing value after %s", loc)
	}
	s := words[wordIdx].Value

	res, err := ctor(s)
	if err != nil {
		return err
	}

	*val = &res
	return nil
}
