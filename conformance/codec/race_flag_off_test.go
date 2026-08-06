//go:build !race

package codec

// raceDetectorEnabled is false in an ordinary (non-race) test build. See
// race_flag_on_test.go's doc for why this pair of files exists.
const raceDetectorEnabled = false
