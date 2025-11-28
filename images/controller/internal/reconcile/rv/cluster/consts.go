package cluster

const (
	MaxNodeId    = uint(7)
	MinNodeMinor = uint(0)
	// MaxNodeMinor is the maximum valid device minor number (inclusive).
	// Value 1048575 = 2^20 - 1 gives us 2^20 total values (0, 1, 2, ..., 1048575).
	// This matches API validation (Maximum=1048575) and Linux kernel limits.
	// SetLowestUnused uses "v <= maxVal", so maxVal is inclusive.
	MaxNodeMinor = uint(1048575)
)
