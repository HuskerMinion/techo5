package update

import (
	"strconv"
	"strings"
)

// Ranking two versions the way Home Assistant does, so the device can act on the same answer the card
// shows. AwesomeVersion, which is what Home Assistant ranks with, compares the dotted numerals as
// numbers, throws away anything after the underscore, and sorts a prerelease below the release of the
// same numbers: v0.7.13 beats v0.7.9, and v0.8.0-beta.4 loses to v0.8.0.
//
// Only versions matching versionPattern get here — ValidVersion refuses the rest before a manifest is
// believed — so this does not have to rank names, dates or tags. Anything it cannot read says so with
// its second result instead of guessing an order, and the caller then leaves the decision alone.

// compareVersions ranks a against b: negative when a is the older, zero when they rank the same,
// positive when a is the newer. The second result is false when either version is not something this
// can rank, in which case the first means nothing.
func compareVersions(a, b string) (int, bool) {
	an, ap, aok := parseVersion(a)
	bn, bp, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}

	// Compare as far as the longer of the two runs: 1.2 and 1.2.0 are the same version, and 1.2.1 is
	// past both.
	for i := 0; i < max(len(an), len(bn)); i++ {
		x, y := at(an, i), at(bn, i)
		if x != y {
			return sign(x - y), true
		}
	}

	switch {
	case ap == "" && bp == "":
		return 0, true
	case ap == "":
		return 1, true // a release is past its own prereleases
	case bp == "":
		return -1, true
	}
	return comparePrerelease(ap, bp), true
}

// parseVersion splits a version into its numbers and its prerelease. Build detail after the underscore
// is dropped, which is where Home Assistant truncates before it compares — two builds of one version
// are one version, and a device running v0.7.13_20260901 is not offered v0.7.13_20260820 as newer.
func parseVersion(v string) (numbers []int, prerelease string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "_")
	v, prerelease, _ = strings.Cut(v, "-")
	if v == "" {
		return nil, "", false
	}

	for part := range strings.SplitSeq(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, "", false
		}
		numbers = append(numbers, n)
	}
	return numbers, prerelease, true
}

// comparePrerelease ranks two prerelease tails field by field, numbers as numbers so beta.10 is past
// beta.9, and a longer tail past a shorter one it shares a prefix with.
func comparePrerelease(a, b string) int {
	af, bf := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(af), len(bf)); i++ {
		switch {
		case i >= len(af):
			return -1
		case i >= len(bf):
			return 1
		}

		x, xerr := strconv.Atoi(af[i])
		y, yerr := strconv.Atoi(bf[i])
		switch {
		case xerr == nil && yerr == nil:
			if x != y {
				return sign(x - y)
			}
		case af[i] != bf[i]:
			// One of them is not a number, so there is nothing to do but take them as text — which is
			// what AwesomeVersion falls back to as well.
			return strings.Compare(af[i], bf[i])
		}
	}
	return 0
}

func at(n []int, i int) int {
	if i < len(n) {
		return n[i]
	}
	return 0
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
