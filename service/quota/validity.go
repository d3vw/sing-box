package quota

import (
	"math"
	"time"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

var validityMonths = map[string]int{
	"month":     1,
	"quarter":   3,
	"half_year": 6,
	"year":      12,
}

// expiresAt is when a user's service period ends: validity months after
// start_date at 00:00 BJT, clamped to the month's last day so a month from
// Jan 31 ends on Feb 28/29. ok is false when no period is configured.
func expiresAt(validity option.QuotaValidity) (at time.Time, ok bool, err error) {
	if validity.StartDate == "" && validity.Validity == "" {
		return time.Time{}, false, nil
	}
	if validity.StartDate == "" || validity.Validity == "" {
		return time.Time{}, false, E.New("start_date and validity must be set together")
	}
	start, err := time.ParseInLocation(time.DateOnly, validity.StartDate, quotaLocation)
	if err != nil {
		return time.Time{}, false, E.Cause(err, "parse start_date")
	}
	months, loaded := validityMonths[validity.Validity]
	if !loaded {
		return time.Time{}, false, E.New("unknown validity: ", validity.Validity, ", expected month, quarter, half_year or year")
	}
	return cycleDay(start.Year(), start.Month()+time.Month(months), start.Day()), true, nil
}

// validUntil is the last day of service, the day before expiry, and how many
// days are left counting today; 0 once expired.
func validUntil(expiry, now time.Time) (lastDay time.Time, daysLeft int) {
	lastDay = expiry.AddDate(0, 0, -1)
	if now.Before(expiry) {
		daysLeft = int(math.Ceil(expiry.Sub(now).Hours() / 24))
	}
	return lastDay, daysLeft
}
