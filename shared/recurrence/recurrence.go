// Package recurrence expands recurrence rules into occurrence dates.
//
// Dates are "YYYY-MM-DD" strings, the same TEXT stored in SQLite, so they
// compare chronologically as strings and never touch a clock or time zone.
package recurrence

import (
	"fmt"
	"strconv"
	"strings"
)

// ExpandRule returns the occurrence dates of rule between from and to,
// inclusive, bounded by the recurrence's startAt and optional endAt.
//
// Only "MONTHLY:<day>" is supported. A day past the end of a month falls on
// that month's last day.
func ExpandRule(rule, startAt string, endAt *string, from, to string) ([]string, error) {
	day, err := monthlyDay(rule)
	if err != nil {
		return nil, err
	}

	year, month, err := yearMonth(max(startAt, from))
	if err != nil {
		return nil, err
	}

	last := to
	if endAt != nil {
		last = min(last, *endAt)
	}

	dates := []string{}
	for {
		date := fmt.Sprintf("%04d-%02d-%02d", year, month, min(day, daysIn(year, month)))
		if date > last {
			break
		}

		if date >= startAt && date >= from {
			dates = append(dates, date)
		}

		year, month = year+month/12, month%12+1
	}

	return dates, nil
}

func monthlyDay(rule string) (int, error) {
	value, ok := strings.CutPrefix(rule, "MONTHLY:")
	day, err := strconv.Atoi(value)
	if !ok || err != nil || day < 1 || day > 31 {
		return 0, fmt.Errorf("unsupported rule %q", rule)
	}

	return day, nil
}

func yearMonth(date string) (int, int, error) {
	var year, month, day int
	if _, err := fmt.Sscanf(date, "%4d-%2d-%2d", &year, &month, &day); err != nil || month < 1 || month > 12 {
		return 0, 0, fmt.Errorf("invalid date %q", date)
	}

	return year, month, nil
}

func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}

		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}
