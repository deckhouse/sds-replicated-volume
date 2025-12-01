package rvstatusconfigsharedsecret

const (
	// RVStatusConfigSharedSecretControllerName is the controller name for rv_status_config_shared_secret controller.
	RVStatusConfigSharedSecretControllerName = "rv_status_config_shared_secret_controller"

	// AlgorithmSHA256 is the SHA256 hashing algorithm for shared secrets.
	AlgorithmSHA256 = "sha256"
	// AlgorithmSHA1 is the SHA1 hashing algorithm for shared secrets.
	AlgorithmSHA1 = "sha1"
)

var algorithms = []string{AlgorithmSHA256, AlgorithmSHA1}
