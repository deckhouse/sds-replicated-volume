package drbdadm

var Command = "drbdadm"

var DumpMDArgs = func(resource string) []string {
	return []string{"dump-md", "--force", resource}
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
	return []string{"create-md", "--max-peers=6", "--force", resource}
}

var DownArgs = func(resource string) []string {
	return []string{"down", resource}
}

var PrimaryArgs = func(resource string) []string {
	return []string{"primary", resource}
}

var PrimaryForceArgs = func(resource string) []string {
	return []string{"primary", "--force", resource}
}

var SecondaryArgs = func(resource string) []string {
	return []string{"secondary", resource}
}

var Events2Args = []string{"events2", "--timestamps"}

var ResizeArgs = func(resource string) []string {
	return []string{"resize", resource}
}
