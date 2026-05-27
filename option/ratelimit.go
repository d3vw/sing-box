package option

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type RateLimit int64

func (r *RateLimit) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch val := v.(type) {
	case float64:
		*r = RateLimit(val)
		return nil
	case string:
		parsed, err := ParseRateLimit(val)
		if err != nil {
			return err
		}
		*r = RateLimit(parsed)
		return nil
	default:
		return fmt.Errorf("invalid rate limit value type: %T", v)
	}
}

func (r RateLimit) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(r))
}

func ParseRateLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Find where the number ends and unit starts
	var numStr, unitStr string
	for i, r := range s {
		if !unicode.IsDigit(r) && r != '.' && r != '-' && r != '+' {
			numStr = s[:i]
			unitStr = s[i:]
			break
		}
	}
	if numStr == "" {
		numStr = s
	}

	numStr = strings.TrimSpace(numStr)
	unitStr = strings.TrimSpace(strings.ToLower(unitStr))

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number in rate limit: %w", err)
	}

	var multiplier float64 = 1
	if unitStr != "" {
		// Rule: If unit contains "bps" or "bit" -> It's bits-per-second (bitrate).
		// Otherwise, it's bytes-per-second (B/s, KB/s, MB/s, GB/s, etc.).
		isBits := strings.Contains(unitStr, "bps") || strings.Contains(unitStr, "bit")

		if isBits {
			if strings.HasPrefix(unitStr, "g") {
				multiplier = 1000.0 * 1000.0 * 1000.0 / 8.0 // Gbps
			} else if strings.HasPrefix(unitStr, "m") {
				multiplier = 1000.0 * 1000.0 / 8.0 // Mbps
			} else if strings.HasPrefix(unitStr, "k") {
				multiplier = 1000.0 / 8.0 // Kbps
			} else {
				multiplier = 1.0 / 8.0 // bps
			}
		} else {
			if strings.HasPrefix(unitStr, "g") {
				multiplier = 1024 * 1024 * 1024 // GB/s
			} else if strings.HasPrefix(unitStr, "m") {
				multiplier = 1024 * 1024 // MB/s
			} else if strings.HasPrefix(unitStr, "k") {
				multiplier = 1024 // KB/s
			} else {
				multiplier = 1 // B/s
			}
		}
	}

	return int64(val * multiplier), nil
}
