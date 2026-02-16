package scheduler

import (
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser supports standard 5-field cron expressions and descriptors like @hourly.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ParseSchedule parses a cron expression and returns a Schedule.
func ParseSchedule(expr string) (cron.Schedule, error) {
	return cronParser.Parse(expr)
}

// NextTime returns the next fire time after the given time for the schedule.
func NextTime(schedule cron.Schedule, after time.Time) time.Time {
	return schedule.Next(after)
}
