package entrysvc

import (
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// Content event type names — the three ratified names emitted from entrysvc.
// They are cms-internal: no shared struct crosses the feature boundary (design
// §4 rule-6 note), consumers subscribe by topic and project metadata only.
const (
	typeContentPublished = "content.published"
	typeContentUpdated   = "content.updated"
	typeContentDeleted   = "content.deleted"

	// aggregateTypeEntry labels every content event's aggregate.
	aggregateTypeEntry = "entry"
)

// ContentPublished is emitted after an entry transitions to published.
type ContentPublished struct {
	sdkevents.BaseEvent
}

// ContentUpdated is emitted after an entry is created, edited, unpublished, or
// has its taxonomy terms replaced.
type ContentUpdated struct {
	sdkevents.BaseEvent
}

// ContentDeleted is emitted after an entry is deleted.
type ContentDeleted struct {
	sdkevents.BaseEvent
}

// newContentPublished builds a content.published event carrying the entry's
// aggregate metadata (aggregate_type "entry", aggregate_id = entryID).
func newContentPublished(entryID string) ContentPublished {
	return ContentPublished{BaseEvent: sdkevents.NewBaseEvent(typeContentPublished).WithAggregate(aggregateTypeEntry, entryID)}
}

// newContentUpdated builds a content.updated event carrying the entry's
// aggregate metadata.
func newContentUpdated(entryID string) ContentUpdated {
	return ContentUpdated{BaseEvent: sdkevents.NewBaseEvent(typeContentUpdated).WithAggregate(aggregateTypeEntry, entryID)}
}

// newContentDeleted builds a content.deleted event carrying the entry's
// aggregate metadata.
func newContentDeleted(entryID string) ContentDeleted {
	return ContentDeleted{BaseEvent: sdkevents.NewBaseEvent(typeContentDeleted).WithAggregate(aggregateTypeEntry, entryID)}
}
