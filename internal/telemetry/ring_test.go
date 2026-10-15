package telemetry

import "testing"

func TestRingWrapsAndNewestFirst(t *testing.T) {
	r := NewRing(3)
	for i := 1; i <= 5; i++ {
		r.Add(RequestRecord{ID: string(rune('a' + i)), Status: i})
	}
	got := r.List()
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	if got[0].ID != "f" || got[1].ID != "e" || got[2].ID != "d" {
		t.Fatalf("order wrong: %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
}
