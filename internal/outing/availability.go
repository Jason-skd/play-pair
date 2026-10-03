package outing

import (
	"slices"
	"strings"
	"time"
)

// ParseDate accepts canonical calendar dates in years 0001 through 9999.
func ParseDate(value string) (Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != value {
		return "", ErrInvalidAvailability
	}
	return Date(value), nil
}

// Location resolves a named timezone without depending on the server's local zone
// or accepting a filesystem path as user input.
func Location(name string) (*time.Location, error) {
	if name == "" || name == "Local" || strings.HasPrefix(name, "/") {
		return nil, ErrInvalidInput
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return location, nil
}

// ParseMinute accepts half-hour boundaries; only an end may be 24:00.
func ParseMinute(value string, end bool) (int, error) {
	if value == "24:00" && end {
		return 1440, nil
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value || parsed.Minute()%30 != 0 {
		return 0, ErrInvalidAvailability
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
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
	for _, interval := range values {
		if !slices.Contains(dates, interval.Date) {
			return MemberInput{}, ErrInvalidAvailability
		}
		if interval.StartMinute < 0 || interval.EndMinute > 1440 || interval.StartMinute >= interval.EndMinute {
			return MemberInput{}, ErrInvalidAvailability
		}
		if interval.StartMinute%30 != 0 || interval.EndMinute%30 != 0 {
			return MemberInput{}, ErrInvalidAvailability
		}
		date, err := time.Parse(time.DateOnly, string(interval.Date))
		if err != nil {
			return MemberInput{}, ErrInvalidAvailability
		}
		for minute := interval.StartMinute; minute <= interval.EndMinute; minute += 30 {
			if !uniqueLocal(date.Add(time.Duration(minute)*time.Minute), loc) {
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
	for _, interval := range values {
		if len(merged) > 0 {
			previous := &merged[len(merged)-1]
			if previous.Date == interval.Date && interval.StartMinute <= previous.EndMinute {
				previous.EndMinute = max(previous.EndMinute, interval.EndMinute)
				continue
			}
		}
		merged = append(merged, interval)
	}
	input.Availability = merged
	return input, nil
}

// Enumerate offsets on nearby zone segments rather than relying on time.Date's
// unspecified choice for repeated or nonexistent wall times.
func uniqueLocal(wall time.Time, loc *time.Location) bool {
	offsets := map[int]bool{}
	start, end := wall.Add(-72*time.Hour), wall.Add(72*time.Hour)
	for candidate := start; candidate.Before(end); {
		local := candidate.In(loc)
		_, offset := local.Zone()
		offsets[offset] = true
		_, next := local.ZoneBounds()
		if next.IsZero() || !next.Before(end) {
			break
		}
		candidate = next
	}
	matches := 0
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second).In(loc)
		sameDate := candidate.Year() == wall.Year() && candidate.Month() == wall.Month() && candidate.Day() == wall.Day()
		sameTime := candidate.Hour() == wall.Hour() && candidate.Minute() == wall.Minute() && candidate.Second() == 0
		if sameDate && sameTime {
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
	for i, interval := range input.Availability {
		if !slices.Contains(plan.Dates, interval.Date) {
			index := i
			warnings = append(warnings, Warning{Code: "date_outside_plan", AvailabilityIndex: &index})
			continue
		}
		valid = append(valid, interval)
	}
	fits := false
	for _, interval := range valid {
		if interval.EndMinute-interval.StartMinute >= plan.DurationMinutes {
			fits = true
			break
		}
	}
	if !fits {
		warnings = append(warnings, Warning{Code: "no_sufficient_availability"})
	}
	return valid, warnings
}
