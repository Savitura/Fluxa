package compliance

import "unicode/utf8"

// levenshteinAtMost returns the edit distance between a and b, or max+1 as
// soon as it can prove the distance exceeds max.
//
// Implemented here rather than pulled in as a dependency: it is ~40 lines,
// and the distance cap makes it strictly faster than a general implementation
// for our only use — "is this name within 2 edits of a sanctioned name",
// asked against thousands of SDN names on the transfer hot path.
func levenshteinAtMost(a, b string, max int) int {
	if a == b {
		return 0
	}
	if max < 0 {
		return 1
	}

	ar := []rune(a)
	br := []rune(b)

	// A length gap alone already exceeds the cap; no need to build the table.
	if diff := len(ar) - len(br); diff > max || -diff > max {
		return max + 1
	}

	// Keep the shorter string on the inner axis so each row is len(br)+1.
	if len(ar) < len(br) {
		ar, br = br, ar
	}

	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		rowMin := curr[0]

		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}

		// Every remaining path runs through this row, so once the best cell
		// in it is already over the cap the final distance must be too.
		if rowMin > max {
			return max + 1
		}
		prev, curr = curr, prev
	}

	if prev[len(br)] > max {
		return max + 1
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// runeLen is a small helper kept next to the matcher so callers can size
// their cap against the shorter of two names without decoding twice.
func runeLen(s string) int { return utf8.RuneCountInString(s) }
