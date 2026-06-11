// Package interval provides half-open time interval algebra: the foundation
// for merging free/busy data across many calendars.
package interval

import (
	"sort"
	"time"
)

// Span is a half-open interval [Start, End). A Span with End <= Start is
// considered empty.
type Span struct {
	Start time.Time
	End   time.Time
}

// IsEmpty reports whether the span covers no time.
func (s Span) IsEmpty() bool { return !s.End.After(s.Start) }

// Duration returns the length of the span (zero for empty spans).
func (s Span) Duration() time.Duration {
	if s.IsEmpty() {
		return 0
	}
	return s.End.Sub(s.Start)
}

// Overlaps reports whether s and t share any instant.
func (s Span) Overlaps(t Span) bool {
	if s.IsEmpty() || t.IsEmpty() {
		return false
	}
	return s.Start.Before(t.End) && t.Start.Before(s.End)
}

// Contains reports whether t lies entirely within s.
func (s Span) Contains(t Span) bool {
	if s.IsEmpty() || t.IsEmpty() {
		return false
	}
	return !t.Start.Before(s.Start) && !t.End.After(s.End)
}

// Pad expands the span by before on the left and after on the right.
func (s Span) Pad(before, after time.Duration) Span {
	return Span{Start: s.Start.Add(-before), End: s.End.Add(after)}
}

// Set is a normalized sequence of spans: sorted by start, non-empty,
// non-overlapping, non-adjacent. Construct with Normalize or the Set methods;
// all methods preserve the invariant.
type Set []Span

// Normalize sorts spans, drops empty ones, and merges overlapping or
// adjacent ones.
func Normalize(spans []Span) Set {
	out := make([]Span, 0, len(spans))
	for _, s := range spans {
		if !s.IsEmpty() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	merged := out[:0]
	for _, s := range out {
		if n := len(merged); n > 0 && !s.Start.After(merged[n-1].End) {
			if s.End.After(merged[n-1].End) {
				merged[n-1].End = s.End
			}
			continue
		}
		merged = append(merged, s)
	}
	return Set(merged)
}

// Union returns all time covered by s or t.
func (s Set) Union(t Set) Set {
	return Normalize(append(append([]Span{}, s...), t...))
}

// Intersect returns all time covered by both s and t.
func (s Set) Intersect(t Set) Set {
	var out []Span
	i, j := 0, 0
	for i < len(s) && j < len(t) {
		start := s[i].Start
		if t[j].Start.After(start) {
			start = t[j].Start
		}
		end := s[i].End
		if t[j].End.Before(end) {
			end = t[j].End
		}
		if end.After(start) {
			out = append(out, Span{start, end})
		}
		if s[i].End.Before(t[j].End) {
			i++
		} else {
			j++
		}
	}
	return Set(out)
}

// Subtract returns the time covered by s but not by t.
func (s Set) Subtract(t Set) Set {
	var out []Span
	j := 0
	for _, sp := range s {
		cur := sp
		for j < len(t) && !t[j].End.After(cur.Start) {
			j++
		}
		k := j
		for k < len(t) && t[k].Start.Before(cur.End) {
			if t[k].Start.After(cur.Start) {
				out = append(out, Span{cur.Start, t[k].Start})
			}
			if t[k].End.After(cur.Start) {
				cur.Start = t[k].End
			}
			if !cur.Start.Before(cur.End) {
				break
			}
			k++
		}
		if cur.Start.Before(cur.End) {
			out = append(out, cur)
		}
	}
	return Set(out)
}

// Complement returns the gaps of s within the window.
func (s Set) Complement(window Span) Set {
	return Set{window}.Subtract(s)
}

// Shrink trims each span by before on the left and after on the right,
// dropping spans that become empty. The inverse of padding: shrinking a free
// set by travel time yields the times at which a padded event still fits.
func (s Set) Shrink(before, after time.Duration) Set {
	var out []Span
	for _, sp := range s {
		t := Span{Start: sp.Start.Add(before), End: sp.End.Add(-after)}
		if !t.IsEmpty() {
			out = append(out, t)
		}
	}
	return Set(out)
}

// ContainsSpan reports whether t lies entirely within a single span of s.
func (s Set) ContainsSpan(t Span) bool {
	for _, sp := range s {
		if sp.Contains(t) {
			return true
		}
		if sp.Start.After(t.Start) {
			break
		}
	}
	return false
}
