package v9

func Keyword[T any, TP SectionPtr[T]]() string {
	return TP(nil).Keyword()
}
