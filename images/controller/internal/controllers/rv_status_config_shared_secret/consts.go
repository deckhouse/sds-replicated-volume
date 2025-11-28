package rvstatusconfigsharedsecret

const (
	// Shared secret hashing algorithms in order of preference
	algorithmSHA256 = "sha256"
	algorithmSHA1   = "sha1"
)

var algorithms = []string{algorithmSHA256, algorithmSHA1}
