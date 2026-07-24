package kernel

import "testing"

func TestToAnySlice(t *testing.T) {
	t.Parallel()

	got := toAnySlice([]int64{1, 2, 3})
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	for i, want := range []int64{1, 2, 3} {
		if got[i] != want {
			t.Errorf("got[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestToAnySlice_empty(t *testing.T) {
	t.Parallel()
	got := toAnySlice([]string{})
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}
