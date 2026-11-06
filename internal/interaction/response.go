package interaction

import (
	"fmt"
	"strings"
)

func ValidateResponse(request Request, reply Response) error {
	switch request.Kind() {
	case Questions:
		if reply.Action == Skip {
			return nil
		}
		if reply.Action != Submit {
			return fmt.Errorf("Invalid answer")
		}
		if len(reply.Answers) != len(request.Questions) {
			return fmt.Errorf("Answer all questions")
		}
		for _, q := range request.Questions {
			if strings.TrimSpace(reply.Answers[q.ID]) == "" {
				return fmt.Errorf("Answer all questions")
			}
		}
	case Form:
		switch reply.Action {
		case Allow, Submit:
			if request.Mode != "url" {
				if reply.Content == nil {
					return fmt.Errorf("Complete the form")
				}
				return ValidateForm(request.RequestedSchema, reply.Content)
			}
		case Decline, Cancel:
		default:
			return fmt.Errorf("Invalid form action")
		}
	default:
		if reply.Action != Allow && reply.Action != Decline && reply.Action != Cancel {
			return fmt.Errorf("Invalid approval action")
		}
	}
	return nil
}
