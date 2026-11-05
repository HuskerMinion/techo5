//go:build !race

package config

// raceSlowdown is how much longer a timing test allows under the race detector, which runs code several
// times slower.
const raceSlowdown = 1
