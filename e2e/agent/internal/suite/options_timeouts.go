package suite

import "time"

// Timeouts holds per-operation timeout durations read from the config file.
// Each field is a string in Go duration format (e.g. "10s", "2m").
type Timeouts struct {
	DRBDRConfigured string `json:"drbdrConfigured"`
	LLVCreated      string `json:"llvCreated"`
}

// DRBDRConfiguredDuration parses and returns the DRBDRConfigured timeout.
func (t Timeouts) DRBDRConfiguredDuration() time.Duration {
	return mustParseDuration(t.DRBDRConfigured)
}

// LLVCreatedDuration parses and returns the LLVCreated timeout.
func (t Timeouts) LLVCreatedDuration() time.Duration {
	return mustParseDuration(t.LLVCreated)
}

func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic("invalid duration: " + s)
	}
	return d
}
