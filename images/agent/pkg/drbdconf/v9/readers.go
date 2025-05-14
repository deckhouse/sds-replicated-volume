package v9

import (
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
)

func ensureLen(words []drbdconf.Word, lenAtLeast int) error {
	if len(words) < lenAtLeast {
		var loc drbdconf.Location
		if len(words) > 0 {
			loc = words[len(words)-1].LocationEnd()
		}
		return fmt.Errorf("%s: missing value", loc)
	}

	return nil
}

func readValue[T any](
	val *T,
	word drbdconf.Word,
	ctor func(string) (T, error),
) error {
	res, err := ctor(word.Value)
	if err != nil {
		return fmt.Errorf("%s: %w", word.Location, err)
	}
	*val = res
	return nil

}

func readValueToPtr[T any](
	valPtr **T,
	word drbdconf.Word,
	ctor func(string) (T, error),
) error {
	tmp := new(T)

	err := readValue(tmp, word, ctor)
	if err != nil {
		return err
	}

	*valPtr = tmp
	return nil
}
