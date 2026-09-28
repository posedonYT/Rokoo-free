package id

import "testing"

func TestSnowflakeMonotonic(t *testing.T) {
	g := New(1)
	prev := g.Next()
	for i := 0; i < 1000; i++ {
		next := g.Next()
		if next <= prev {
			t.Fatalf("id not increasing: %d <= %d", next, prev)
		}
		prev = next
	}
	if _, err := Parse(Format(prev)); err != nil {
		t.Fatal(err)
	}
}
