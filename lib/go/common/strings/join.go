package strings

import (
	"slices"
	"strings"

	uiter "github.com/deckhouse/sds-common-lib/utils/iter"
)

type GetNamer interface {
	GetName() string
}

func JoinNames[T GetNamer](items []T, sep string) string {
	return strings.Join(
		slices.Collect(
			uiter.Map(
				slices.Values(items),
				func(item T) string {
					return item.GetName()
				},
			),
		),
		sep,
	)
}

func JoinNonEmpty(sep string, elems ...string) string {
	return strings.Join(
		slices.Collect(
			uiter.Filter(
				slices.Values(elems),
				func(s string) bool { return s != "" }),
		),
		sep,
	)
}
