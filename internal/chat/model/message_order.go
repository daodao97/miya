package model

func SameMessageOrder(messages []Message, order []int) bool {
	if len(messages) != len(order) {
		return false
	}
	for i, m := range messages {
		if m.ID != order[i] {
			return false
		}
	}
	return true
}
