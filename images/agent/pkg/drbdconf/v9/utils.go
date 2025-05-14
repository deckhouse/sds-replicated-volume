package v9

func Keyword[T any, TP KeyworderPtr[T]]() string {
	return TP(nil).Keyword()
}
