package drbdconf

import "fmt"

func ensureLen(words []Word, lenAtLeast int) error {
	if len(words) < lenAtLeast {
		var loc Location
		if len(words) > 0 {
			loc = words[len(words)-1].LocationEnd()
		}
		return fmt.Errorf("%s: missing value", loc)
	}

	return nil
}
