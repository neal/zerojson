package main

import "testing"

func TestFuzzTypeNames(t *testing.T) {
	seen := map[string]bool{}
	for name, want := range map[string]string{"Hidden": "Hidden", "hidden": "_hidden", "_hidden": "__hidden", "é": "_é", "É": "É", "猫": "_猫"} {
		got := fuzzTypeName(name)
		if got != want || seen[got] {
			t.Fatalf("%s: got %s want unique %s", name, got, want)
		}
		seen[got] = true
	}
}
