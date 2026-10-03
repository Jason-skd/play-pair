package outing

import (
	"slices"
	"strings"
	"time"
)

func ParseDate(s string) (Date, error) {
	t, e := time.Parse(time.DateOnly, s)
	if e != nil || t.Year() < 1 || t.Format(time.DateOnly) != s {
		return "", ErrInvalidAvailability
	}
	return Date(s), nil
}
func Location(name string) (*time.Location, error) {
	if name == "" || name == "Local" || strings.HasPrefix(name, "/") {
		return nil, ErrInvalidInput
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return l, nil
}

// ParseMinute accepts half-hour boundaries; only an end may be 24:00.
func ParseMinute(s string, end bool) (int, error) {
	if s == "24:00" && end {
		return 1440, nil
	}
	t, err := time.Parse("15:04", s)
	if err != nil || t.Format("15:04") != s || t.Minute()%30 != 0 {
		return 0, ErrInvalidAvailability
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Normalize validates every half-hour boundary, including DST gaps and folds,
// before merging overlapping intervals. It never splits invalid input.
func Normalize(input MemberInput, dates []Date, timezone string) (MemberInput, error) {
	if input.MaxBudgetFen < 0 {
		return MemberInput{}, ErrInvalidInput
	}
	if len(input.Availability) == 0 || len(input.Availability) > 256 {
		return MemberInput{}, ErrInvalidAvailability
	}
	loc, err := Location(timezone)
	if err != nil {
		return MemberInput{}, err
	}
	values := slices.Clone(input.Availability)
	for _, v := range values {
		if !slices.Contains(dates, v.Date) || v.StartMinute < 0 || v.EndMinute > 1440 || v.StartMinute >= v.EndMinute || v.StartMinute%30 != 0 || v.EndMinute%30 != 0 {
			return MemberInput{}, ErrInvalidAvailability
		}
		d, err := time.Parse(time.DateOnly, string(v.Date))
		if err != nil {
			return MemberInput{}, ErrInvalidAvailability
		}
		for m := v.StartMinute; m <= v.EndMinute; m += 30 {
			if !uniqueLocal(d.Add(time.Duration(m)*time.Minute), loc) {
				return MemberInput{}, ErrInvalidAvailability
			}
		}
	}
	slices.SortFunc(values, func(a, b Availability) int {
		if a.Date < b.Date {
			return -1
		}
		if a.Date > b.Date {
			return 1
		}
		return a.StartMinute - b.StartMinute
	})
	merged := make([]Availability, 0, len(values))
	for _, v := range values {
		if len(merged) > 0 {
			p := &merged[len(merged)-1]
			if p.Date == v.Date && v.StartMinute <= p.EndMinute {
				p.EndMinute = max(p.EndMinute, v.EndMinute)
				continue
			}
		}
		merged = append(merged, v)
	}
	input.Availability = merged
	return input, nil
}

// Enumerate offsets on nearby zone segments rather than relying on time.Date's
// unspecified choice for repeated or nonexistent wall times.
func uniqueLocal(wall time.Time, loc *time.Location) bool {
	offsets := map[int]bool{}
	start, end := wall.Add(-72*time.Hour), wall.Add(72*time.Hour)
	for t := start; t.Before(end); {
		local := t.In(loc)
		_, offset := local.Zone()
		offsets[offset] = true
		_, next := local.ZoneBounds()
		if next.IsZero() || !next.Before(end) {
			break
		}
		t = next
	}
	matches := 0
	for offset := range offsets {
		t := wall.Add(-time.Duration(offset) * time.Second).In(loc)
		if t.Year() == wall.Year() && t.Month() == wall.Month() && t.Day() == wall.Day() && t.Hour() == wall.Hour() && t.Minute() == wall.Minute() && t.Second() == 0 {
			matches++
		}
	}
	return matches == 1
}

// ValidAvailability preserves stored input and reports only current-plan conflicts.
// Decision aggregation can consume the same valid intervals without treating an
// ignored warning as confirmation of an invalid interval.
func ValidAvailability(input MemberInput, plan Plan) ([]Availability, []Warning) {
	valid := []Availability{}
	warnings := []Warning{}
	for i, a := range input.Availability {
		if !slices.Contains(plan.Dates, a.Date) {
			n := i
			warnings = append(warnings, Warning{Code: "date_outside_plan", AvailabilityIndex: &n})
			continue
		}
		valid = append(valid, a)
	}
	fits := false
	for _, a := range valid {
		if a.EndMinute-a.StartMinute >= plan.DurationMinutes {
			fits = true
			break
		}
	}
	if !fits {
		warnings = append(warnings, Warning{Code: "no_sufficient_availability"})
	}
	return valid, warnings
}
