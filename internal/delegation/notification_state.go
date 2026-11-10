package delegation

// NotifyState is where a completion notification stands, for one watch
// (task, receiver) and for the Delivery that batches watches to one receiver.
// The string values are persisted and returned by MCP tools; keep them.
//
//	watching ─finish─▶ pending ─claim─▶ submitting ─accepted─▶ delivered
//	   │                 │  ▲               │
//	 unwatch          expire └not_started────┤
//	   ▼                 ▼                  unknown
//	cancelled         expired               ▼
//	                                   needsAttention ─abandon─▶ cancelled (watch)
//
// Only watches use NotifyWatching; deliveries start at NotifyPending.
type NotifyState string

const (
	NotifyWatching       NotifyState = "watching"
	NotifyPending        NotifyState = "pending"
	NotifySubmitting     NotifyState = "submitting"
	NotifyDelivered      NotifyState = "delivered"
	NotifyCancelled      NotifyState = "cancelled"
	NotifyExpired        NotifyState = "expired"
	NotifyNeedsAttention NotifyState = "needsAttention"
)

// Live reports whether the watch still expects to notify its receiver.
func (s NotifyState) Live() bool {
	return s == NotifyWatching || s == NotifyPending || s.InFlight()
}

// InFlight reports whether input may have reached the receiver and the
// native outcome is not yet known to be a delivery.
func (s NotifyState) InFlight() bool { return s == NotifySubmitting || s == NotifyNeedsAttention }

// Started reports whether the notification can no longer be withdrawn.
func (s NotifyState) Started() bool { return s.InFlight() || s == NotifyDelivered }

func (s NotifyState) validDelivery() bool {
	switch s {
	case NotifyPending, NotifySubmitting, NotifyDelivered, NotifyCancelled, NotifyExpired, NotifyNeedsAttention:
		return true
	}
	return false
}

func (s NotifyState) validWatch() bool { return s == NotifyWatching || s.validDelivery() }

// SubmissionPhase records how far native input got, so recovery after a
// crash knows whether the receiver may already have seen it. "" predates it.
type SubmissionPhase string

const (
	// PhasePrepared: reserved, nothing written to the native session yet.
	PhasePrepared SubmissionPhase = "prepared"
	// PhaseSubmitting: input may have been written; never replay blindly.
	PhaseSubmitting SubmissionPhase = "submitting"
)

func (p SubmissionPhase) valid() bool { return p == "" || p == PhasePrepared || p == PhaseSubmitting }

// ResolutionAbandoned is the only Task.Resolution and Delivery.Resolution:
// tracking stopped while the native outcome stayed unknown.
const ResolutionAbandoned = "abandoned"

// Result.Availability values.
const (
	ResultPending     = "pending"
	ResultAvailable   = "available"
	ResultUnavailable = "unavailable"
	ResultExpired     = "expired"
)
