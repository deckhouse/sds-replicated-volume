package drbdadm

var Command = "drbdadm"

var DumpMDArgs = func(resource string) []string {
	return []string{"dump-md", resource}
}

var StatusArgs = func(resource string) []string {
	return []string{"status", resource}
}

var UpArgs = func(resource string) []string {
	return []string{"up", resource}
}

var AdjustArgs = func(resource string) []string {
	return []string{"adjust", resource}
}

var CreateMDArgs = func(resource string) []string {
	return []string{"create-md", "--force", resource}
}

var Events2Args = []string{"events2", "--timestamps"}
