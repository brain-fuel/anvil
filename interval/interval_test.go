package interval

import (
	"testing"
	"time"
)

var base = time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return base.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}
func sp(h1, m1, h2, m2 int) Span { return Span{at(h1, m1), at(h2, m2)} }
func eq(a, b Set) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Start.Equal(b[i].Start) || !a[i].End.Equal(b[i].End) {
			return false
		}
	}
	return true
}

func TestNormalizeMergesOverlapAndAdjacent(t *testing.T) {
	got := Normalize([]Span{sp(13, 0, 14, 0), sp(9, 0, 10, 0), sp(10, 0, 11, 0), sp(13, 30, 15, 0), sp(8, 0, 8, 0)})
	want := Set{sp(9, 0, 11, 0), sp(13, 0, 15, 0)}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestIntersect(t *testing.T) {
	a := Set{sp(9, 0, 12, 0), sp(13, 0, 17, 0)}
	b := Set{sp(11, 0, 14, 0), sp(16, 0, 18, 0)}
	want := Set{sp(11, 0, 12, 0), sp(13, 0, 14, 0), sp(16, 0, 17, 0)}
	if got := a.Intersect(b); !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSubtract(t *testing.T) {
	a := Set{sp(9, 0, 17, 0)}
	b := Set{sp(8, 0, 9, 30), sp(12, 0, 13, 0), sp(16, 30, 18, 0)}
	want := Set{sp(9, 30, 12, 0), sp(13, 0, 16, 30)}
	if got := a.Subtract(b); !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSubtractSwallowsWholeSpan(t *testing.T) {
	a := Set{sp(9, 0, 10, 0), sp(11, 0, 12, 0)}
	b := Set{sp(8, 0, 13, 0)}
	if got := a.Subtract(b); len(got) != 0 {
		t.Fatalf("got %v want empty", got)
	}
}

func TestComplement(t *testing.T) {
	busy := Set{sp(10, 0, 11, 0), sp(14, 0, 15, 0)}
	want := Set{sp(9, 0, 10, 0), sp(11, 0, 14, 0), sp(15, 0, 17, 0)}
	if got := busy.Complement(sp(9, 0, 17, 0)); !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestShrink(t *testing.T) {
	free := Set{sp(9, 0, 10, 0), sp(12, 0, 17, 0)}
	want := Set{sp(12, 30, 16, 30)}
	if got := free.Shrink(30*time.Minute, 30*time.Minute); !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestContainsSpan(t *testing.T) {
	s := Set{sp(9, 0, 12, 0), sp(13, 0, 17, 0)}
	if !s.ContainsSpan(sp(13, 0, 14, 0)) {
		t.Fatal("expected contained")
	}
	if s.ContainsSpan(sp(11, 30, 13, 30)) {
		t.Fatal("expected not contained across gap")
	}
}
