package memory

// JobState is an extraction job's lifecycle. Values are persisted.
//
//	pending ─claim─▶ running ─saved─▶ completed
//	   ▲               │ └─error, no retries left─▶ failed
//	   └──retry/restart┘
//
// Disabling memory or forgetting a referenced entry finishes an open job as
// canceled; a result computed against changed memory finishes it as stale.
type JobState string

const (
	JobPending   JobState = "pending"
	JobRunning   JobState = "running"
	JobCompleted JobState = "completed"
	JobFailed    JobState = "failed"
	JobCanceled  JobState = "canceled"
	JobStale     JobState = "stale"
)

// Open reports whether the job still holds evidence and may run.
func (s JobState) Open() bool { return s == JobPending || s == JobRunning }

// Entry.State values. The first five are stored; EntryExpired and
// EntryUnavailable are derived for readers and never persisted.
const (
	EntryActive      = "active"
	EntrySuperseded  = "superseded"
	EntryForgotten   = "forgotten"
	EntryPromoted    = "promoted"
	EntryConflicted  = "conflicted"
	EntryExpired     = "expired"
	EntryUnavailable = "unavailable"
)
