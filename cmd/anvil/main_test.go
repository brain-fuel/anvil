package main

import (
	"strings"
	"testing"
	"time"
)

func TestValidateFindFlags(t *testing.T) {
	if err := validateFindFlags(45*time.Minute, 15*time.Minute, 0, 10); err != nil {
		t.Fatalf("valid flags returned error: %v", err)
	}

	cases := []struct {
		name string
		err  string
		d    time.Duration
		step time.Duration
		trav time.Duration
		n    int
	}{
		{name: "duration", err: "-d must be greater than zero", d: 0, step: time.Minute, n: 1},
		{name: "step", err: "-step must be greater than zero", d: time.Minute, step: 0, n: 1},
		{name: "travel", err: "-travel cannot be negative", d: time.Minute, step: time.Minute, trav: -time.Minute, n: 1},
		{name: "limit", err: "-n must be greater than zero", d: time.Minute, step: time.Minute, n: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFindFlags(tc.d, tc.step, tc.trav, tc.n)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("error = %v, want containing %q", err, tc.err)
			}
		})
	}
}
