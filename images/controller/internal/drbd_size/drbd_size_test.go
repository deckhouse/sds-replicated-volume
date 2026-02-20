package drbd_size

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestLowerVolumeSize(t *testing.T) {
	tests := []struct {
		name       string
		usableSize string
	}{
		{name: "1Gi", usableSize: "1Gi"},
		{name: "10Gi", usableSize: "10Gi"},
		{name: "100Gi", usableSize: "100Gi"},
		{name: "1Ti", usableSize: "1Ti"},
		{name: "10Ti", usableSize: "10Ti"},
		{name: "1Mi", usableSize: "1Mi"},
		{name: "512Ki", usableSize: "512Ki"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usable := resource.MustParse(tt.usableSize)
			lower := LowerVolumeSize(usable)

			// Lower must be >= usable (metadata adds overhead).
			if lower.Cmp(usable) < 0 {
				t.Errorf("LowerVolumeSize(%s) = %s, want >= %s",
					usable.String(), lower.String(), usable.String())
			}

			// Lower must be aligned to 8 sectors (4096 bytes).
			if lower.Value()%(8*512) != 0 {
				t.Errorf("LowerVolumeSize(%s) = %s, not aligned to 4Ki",
					usable.String(), lower.String())
			}
		})
	}
}

func TestUsableSize(t *testing.T) {
	tests := []struct {
		name      string
		lowerSize string
	}{
		{name: "1Gi", lowerSize: "1Gi"},
		{name: "10Gi", lowerSize: "10Gi"},
		{name: "100Gi", lowerSize: "100Gi"},
		{name: "1Ti", lowerSize: "1Ti"},
		{name: "10Ti", lowerSize: "10Ti"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lower := resource.MustParse(tt.lowerSize)
			usable := UsableSize(lower)

			// Usable must be < lower (metadata takes space).
			if usable.Cmp(lower) >= 0 {
				t.Errorf("UsableSize(%s) = %s, want < %s",
					lower.String(), usable.String(), lower.String())
			}

			// Usable must be positive.
			if usable.Value() <= 0 {
				t.Errorf("UsableSize(%s) = %s, want > 0",
					lower.String(), usable.String())
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		usableSize string
	}{
		{name: "1Gi", usableSize: "1Gi"},
		{name: "10Gi", usableSize: "10Gi"},
		{name: "100Gi", usableSize: "100Gi"},
		{name: "1Ti", usableSize: "1Ti"},
		{name: "10Ti", usableSize: "10Ti"},
		{name: "1Mi", usableSize: "1Mi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usable := resource.MustParse(tt.usableSize)

			// usable -> lower -> usable' should give usable' >= usable
			// because LowerVolumeSize rounds up for alignment.
			lower := LowerVolumeSize(usable)
			usableBack := UsableSize(lower)

			if usableBack.Cmp(usable) < 0 {
				t.Errorf("Round-trip: UsableSize(LowerVolumeSize(%s)) = %s, want >= %s",
					usable.String(), usableBack.String(), usable.String())
			}
		})
	}
}

func TestMinimumViableLowerSize(t *testing.T) {
	// DRBD metadata requires at least ~40Ki (superblock + AL + minimal bitmap).
	// UsableSize for smaller lower volumes would underflow.
	// This test documents the minimum viable lower volume size.

	// Find minimum size where UsableSize returns positive value.
	var minViable int64
	for size := int64(32 * 1024); size <= 128*1024; size += 4096 {
		q := *resource.NewQuantity(size, resource.BinarySI)
		usable := UsableSize(q)
		if usable.Value() > 0 && usable.Value() < size {
			minViable = size
			break
		}
	}

	if minViable == 0 {
		t.Fatal("Could not find minimum viable lower size in range 32Ki-128Ki")
	}

	t.Logf("Minimum viable lower volume size: %d bytes (%s)",
		minViable, resource.NewQuantity(minViable, resource.BinarySI).String())

	// Verify LowerVolumeSize(1Ki) produces at least minViable.
	tiny := resource.MustParse("1Ki")
	lower := LowerVolumeSize(tiny)
	if lower.Value() < minViable {
		t.Errorf("LowerVolumeSize(1Ki) = %s, want >= %d bytes",
			lower.String(), minViable)
	}
}

func TestMetadataOverhead(t *testing.T) {
	// Verify metadata overhead is reasonable (< 1% for large volumes).
	tests := []struct {
		name           string
		lowerSize      string
		maxOverheadPct float64
	}{
		{name: "1Gi_5pct", lowerSize: "1Gi", maxOverheadPct: 5.0},
		{name: "10Gi_1pct", lowerSize: "10Gi", maxOverheadPct: 1.0},
		{name: "100Gi_0.5pct", lowerSize: "100Gi", maxOverheadPct: 0.5},
		{name: "1Ti_0.1pct", lowerSize: "1Ti", maxOverheadPct: 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lower := resource.MustParse(tt.lowerSize)
			usable := UsableSize(lower)

			overhead := float64(lower.Value()-usable.Value()) / float64(lower.Value()) * 100
			if overhead > tt.maxOverheadPct {
				t.Errorf("Overhead for %s is %.2f%%, want <= %.2f%%",
					lower.String(), overhead, tt.maxOverheadPct)
			}
		})
	}
}
