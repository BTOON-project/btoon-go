package btoon

import "strconv"

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func utoa(u uint64) string { return strconv.FormatUint(u, 10) }

// quote quotes s for an error message, shortening long strings.
func quote(s string) string {
	if len(s) > 64 {
		s = s[:64] + "..."
	}
	return strconv.Quote(s)
}
