package delegation

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
)

func (m *Manager) CheckCreateRequest(caller, requestID string) error {
	if _, exists := m.view.Load().Operations[caller+"\n"+requestID]; exists {
		return errCode("REQUEST_CONFLICT", "request_id belongs to a different operation")
	}
	return nil
}

func (m *Manager) RecheckOutcome(id string, failure string) error {
	return m.change(func(s *Snapshot) error {
		t, ok := s.Tasks[id]
		if !ok {
			return errCode("TASK_NOT_FOUND", "Unknown task")
		}
		if Terminal(t.State) {
			failure = ""
		}
		t.RecheckError, _ = bounded(failure, 4096)
		s.Tasks[id] = t
		return nil
	})
}

// Mutate atomically commits the operation's receipt and state change. fresh is
// false for a retry even if newer operations have since changed the task.
func (m *Manager) Mutate(op Operation) (receipt Receipt, fresh bool, err error) {
	if _, ok := referenceSource(op.Caller); !ok || strings.TrimSpace(op.RequestID) == "" || len(op.RequestID) > 128 || len(op.Reason) > 8000 {
		return receipt, false, errCode("INVALID_ARGUMENT", "Invalid operation identity or reason")
	}
	op.Reason = strings.TrimSpace(op.Reason)
	if op.DeliveryID != "" && op.Name != "abandon_delivery" {
		return receipt, false, errCode("INVALID_ARGUMENT", "Unexpected notification identity")
	}
	data, _ := json.Marshal(op)
	hash := digest(data)
	if previous, ok := m.view.Load().Operations[op.Caller+"\n"+op.RequestID]; ok {
		if previous.Hash != hash {
			return Receipt{}, false, errCode("REQUEST_CONFLICT", "request_id belongs to different operation parameters")
		}
		previous.TaskIDs = slices.Clone(previous.TaskIDs)
		return previous, false, nil
	}
	err = m.change(func(s *Snapshot) error {
		key := op.Caller + "\n" + op.RequestID
		if previous, ok := s.Operations[key]; ok {
			if previous.Hash != hash {
				return errCode("REQUEST_CONFLICT", "request_id belongs to different operation parameters")
			}
			receipt = previous
			return nil
		}
		for _, task := range s.Tasks {
			if task.From == op.Caller && task.RequestID == op.RequestID {
				return errCode("REQUEST_CONFLICT", "request_id belongs to task creation")
			}
		}
		var notification string
		var taskIDs []string
		var err error
		switch op.Name {
		case "cancel_task":
			err = m.cancel(s, op.Caller, op.TaskID)
		case "watch_task":
			notification, err = m.watch(s, op.Caller, op.TaskID)
		case "unwatch_task":
			err = m.unwatch(s, op.Caller, op.TaskID)
			notification = string(NotifyCancelled)
		case "abandon_delivery":
			err = m.abandonDelivery(s, op)
			notification = ResolutionAbandoned
		case "abandon":
			err = m.abandon(s, op.Caller, op.TaskID, op.Reason)
		case "recheck":
			t, ok := s.Tasks[op.TaskID]
			if !ok {
				err = errCode("TASK_NOT_FOUND", "Unknown task")
			} else if t.From != op.Caller {
				err = errCode("ACCESS_DENIED", "Only the initiating conversation can recheck")
			}
		case "cancel_all":
			if op.TaskID != "" || op.Reason != "" {
				return errCode("INVALID_ARGUMENT", "Invalid cancellation scope")
			}
			for id, t := range s.Tasks {
				if !Terminal(t.State) && ancestor(*s, op.Caller, t.From) {
					taskIDs = append(taskIDs, id)
				}
			}
			sort.Strings(taskIDs)
			for _, id := range taskIDs {
				if err := m.cancel(s, "", id); err != nil {
					return err
				}
			}
		default:
			err = errCode("INVALID_ARGUMENT", "Unknown task operation")
		}
		if err != nil {
			return err
		}
		receipt = Receipt{Operation: op, Hash: hash, Accepted: true, Notification: notification, AcceptedAt: m.now(), TaskIDs: taskIDs}
		s.Operations[key] = receipt
		fresh = true
		return nil
	})
	if err != nil {
		return Receipt{}, false, err
	}
	receipt.TaskIDs = slices.Clone(receipt.TaskIDs)
	return receipt, fresh, nil
}
