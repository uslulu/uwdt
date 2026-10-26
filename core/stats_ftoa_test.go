package core

import "testing"

func TestFtoaLeadingZero(t *testing.T) {
	for in, want := range map[float64]string{0.08: "0.08", 10.05: "10.05", 7.21: "7.21", 3: "3.00"} {
		if got := ftoa(in); got != want {
			t.Errorf("ftoa(%v) = %q, want %q", in, got, want)
		}
	}
}
