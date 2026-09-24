package generator

import (
	"os"
	"reflect"
	"testing"
)

// Child go test processes do not inherit their parent's instrumentation.
// Keep CI inheritance and campaign-specific opt-ins in one place.
func fixtureTestFlags(t *testing.T, extraRace, extraCheckptr bool) []string {
	t.Helper()
	flags := fixtureInstrumentation(raceDetectorEnabled || extraRace,
		os.Getenv("ZEROJSON_CHECKPTR") != "" || extraCheckptr)
	t.Logf("fixture instrumentation: %v", flags)
	return flags
}

func fixtureInstrumentation(race, checkptr bool) []string {
	var flags []string
	if race {
		flags = append(flags, "-race")
	}
	if checkptr {
		flags = append(flags, "-gcflags=all=-d=checkptr=2")
	}
	return flags
}

func TestFixtureInstrumentation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		race, checkptr bool
		want           []string
	}{
		{"ordinary", false, false, nil},
		{"race", true, false, []string{"-race"}},
		{"checkptr", false, true, []string{"-gcflags=all=-d=checkptr=2"}},
		{"both", true, true, []string{"-race", "-gcflags=all=-d=checkptr=2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixtureInstrumentation(tc.race, tc.checkptr); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("instrumentation = %v, want %v", got, tc.want)
			}
			for _, setting := range []string{"", "1"} {
				t.Setenv("ZEROJSON_CHECKPTR", setting)
				want := fixtureInstrumentation(raceDetectorEnabled || tc.race, setting != "" || tc.checkptr)
				if got := fixtureTestFlags(t, tc.race, tc.checkptr); !reflect.DeepEqual(got, want) {
					t.Fatalf("inherited instrumentation = %v, want %v", got, want)
				}
			}
		})
	}
}
