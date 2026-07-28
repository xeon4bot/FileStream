package utils

import (
	"fmt"
)

// FormatBytes formats a byte count into a human-readable string (B, KB, MB, GB, etc.)
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatDuration formats duration in seconds into a "Xh Ym Zs" or "Ym Zs" string
func FormatDuration(seconds int) string {
	if seconds <= 0 {
		return "N/A"
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}
