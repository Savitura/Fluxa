package compliance

import "testing"

func TestLevenshteinAtMost(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		max  int
		want int
	}{
		{"identical", "zawahiri", "zawahiri", 2, 0},
		{"one substitution", "zawahiri", "zawahiro", 2, 1},
		{"one deletion", "zawahiri", "zawahir", 2, 1},
		{"one insertion", "zawahir", "zawahiri", 2, 1},
		{"two edits", "zawahiri", "zawahri", 2, 1},
		{"transposition costs two", "ab", "ba", 2, 2},
		{"distance three exceeds cap of two", "kitten", "sitting", 2, 3},
		{"distance three within cap of three", "kitten", "sitting", 3, 3},
		{"empty against non-empty", "", "abc", 5, 3},
		{"both empty", "", "", 2, 0},
		{"length gap alone exceeds cap", "a", "abcdefgh", 2, 3},
		{"unicode runes not bytes", "zawahirí", "zawahiri", 2, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := levenshteinAtMost(tc.a, tc.b, tc.max)
			// Beyond the cap the function only promises "> max", not the exact
			// distance, so assert the cap semantics rather than the number.
			if tc.want > tc.max {
				if got <= tc.max {
					t.Fatalf("levenshteinAtMost(%q,%q,%d) = %d, want > %d", tc.a, tc.b, tc.max, got, tc.max)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("levenshteinAtMost(%q,%q,%d) = %d, want %d", tc.a, tc.b, tc.max, got, tc.want)
			}
		})
	}
}

func TestLevenshteinAtMostIsSymmetric(t *testing.T) {
	pairs := [][2]string{
		{"quiet shipping llc", "quiet shipping ltd"},
		{"maria sanchez", "maria sanchz"},
		{"short", "a much longer string entirely"},
	}
	for _, p := range pairs {
		if got, want := levenshteinAtMost(p[0], p[1], 4), levenshteinAtMost(p[1], p[0], 4); got != want {
			t.Fatalf("asymmetric distance for %q/%q: %d vs %d", p[0], p[1], got, want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Al-Zawahiri, Ayman", "al zawahiri ayman"},
		{"  QUIET   SHIPPING  LLC  ", "quiet shipping llc"},
		{"O'Brien & Sons, Inc.", "o brien sons inc"},
		{"", ""},
		{"---", ""},
	}
	for _, tc := range tests {
		if got := normalizeName(tc.in); got != tc.want {
			t.Fatalf("normalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRuneLen(t *testing.T) {
	if got := runeLen("zawahirí"); got != 8 {
		t.Fatalf("runeLen = %d, want 8", got)
	}
}
