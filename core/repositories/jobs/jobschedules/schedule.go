package jobschedules

import (
	"encoding/json"
	"time"

	"github.com/gopernicus/gopernicus/core/jobs/scheduler"
)

// compile-time check: JobSchedule satisfies scheduler.Schedule.
var _ scheduler.Schedule = JobSchedule{}

// GetScheduleID implements scheduler.Schedule.
func (s JobSchedule) GetScheduleID() string { return s.ScheduleID }

// GetName implements scheduler.Schedule.
func (s JobSchedule) GetName() string { return s.Name }

// GetEventType implements scheduler.Schedule.
func (s JobSchedule) GetEventType() string { return s.EventType }

// GetCronExpr implements scheduler.Schedule.
func (s JobSchedule) GetCronExpr() string { return s.CronExpr }

// GetPayload implements scheduler.Schedule.
func (s JobSchedule) GetPayload() json.RawMessage { return s.Payload }

// GetNextRunAt implements scheduler.Schedule.
func (s JobSchedule) GetNextRunAt() time.Time { return s.NextRunAt }
