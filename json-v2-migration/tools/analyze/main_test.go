package main

import (
	"math"
	"testing"
)

func TestSignTest(t *testing.T) {
	for _, c := range []struct {
		k, m int
		want float64
	}{
		{10, 0, 2.0 / 1024},
		{0, 10, 2.0 / 1024},
		{5, 5, 1},
		{9, 1, 2 * 11.0 / 1024},
		{0, 0, 1},
	} {
		if got := signTest(c.k, c.m); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("signTest(%d, %d) = %v, want %v", c.k, c.m, got, c.want)
		}
	}
}

func TestPairedTestPairsByRound(t *testing.T) {
	// Each round is slower or faster for both variants alike; the second
	// variant is half the first in every round.
	from := []float64{1, 4, 1, 4, 1, 4}
	to := []float64{0.5, 2, 0.5, 2, 0.5, 2}
	r, p, n := pairedTest(from, to)
	if n != 6 || r != 0.5 || math.Abs(p-2.0/64) > 1e-12 {
		t.Fatalf("pairedTest = %v, %v, %d", r, p, n)
	}
	if _, _, n := pairedTest(from, to[:5]); n != 0 {
		t.Fatalf("unequal samples paired %d rounds", n)
	}
}
