package metrics

import "testing"

func TestWindowPercentiles(t *testing.T) {
	w := NewWindow(4)
	if n, _ := w.Percentiles(50); n != 0 {
		t.Fatalf("empty window has %d samples", n)
	}
	for _, v := range []float64{10, 20, 30} {
		w.Add(v)
	}
	if n, p := w.Percentiles(0, 50, 100); n != 3 || p[0] != 10 || p[1] != 20 || p[2] != 30 {
		t.Fatalf("got n=%d p=%v", n, p)
	}
	// Overflow: the oldest samples (10, 20) are replaced by 40, 50.
	w.Add(40)
	w.Add(50)
	w.Add(60)
	if n, p := w.Percentiles(0, 100); n != 4 || p[0] != 30 || p[1] != 60 {
		t.Fatalf("after wrap: n=%d p=%v, want 4 samples from 30 to 60", n, p)
	}
}
