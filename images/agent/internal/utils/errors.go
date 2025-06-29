package utils

import (
	"errors"
	"fmt"
)

var ErrUnexpectedReturnWithoutError = errors.New(
	"function unexpectedly returned without error",
)

func RecoverPanicToErr(err *error) {
	v := recover()
	if v == nil {
		return
	}

	var verr error
	switch vt := v.(type) {
	case string:
		verr = errors.New(vt)
	case error:
		verr = vt
	default:
		verr = errors.New(fmt.Sprint(v))
	}

	verr = errors.Join(*err, verr)

	*err = fmt.Errorf("recovered from panic: %w", verr)
}
