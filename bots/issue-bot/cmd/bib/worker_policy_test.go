package main

import (
	"reflect"
	"testing"
)

func TestAppendLabelsKeepsDefaultsAndDropsDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		extra    []string
		want     []string
	}{
		{"nothing to add", []string{"keep"}, nil, []string{"keep"}},
		{"adds a new label", []string{"keep"}, []string{"extra"}, []string{"keep", "extra"}},
		{"ignores a repeat", []string{"keep"}, []string{"KEEP"}, []string{"keep"}},
		{"trims and skips blanks", nil, []string{"  spaced  ", "", "   "}, []string{"spaced"}},
		{"collapses repeats inside the filter", nil, []string{"one", "One"}, []string{"one"}},
	} {
		if got := appendLabels(tc.existing, tc.extra); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
