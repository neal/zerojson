//go:build race

package codec

// raceDetectorEnabled reports whether this test binary was built with
// `go test -race` — Go's toolchain adds the implicit `race` build
// constraint automatically whenever -race is passed, so this pair of
// files (race_flag_on_test.go / race_flag_off_test.go) is the standard
// way to detect it at compile time without an internal-only API. See
// allocation_test.go's use: testing.AllocsPerRun's exact counts are not
// race-detector-stable for any path that touches a map or an interface
// value (the race detector's shadow-memory bookkeeping for those
// allocates on its own), so the non-zero allocation-budget assertions
// there relax to a separately pinned race-build count instead of
// skipping the check outright.
const raceDetectorEnabled = true
