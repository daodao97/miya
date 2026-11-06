package protocol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"miya/internal/interaction"
	"miya/internal/interaction/legacy"
	"miya/internal/textutil"
)

func ApprovalFingerprint(a interaction.Request) string {
	data, _ := json.Marshal(a)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// validateMobileForm uses exactly the desktop form contract. The UI renders
// its supported primitive fields; validation remains authoritative here.
func ValidateMobileForm(a interaction.Request, fields map[string]string) (map[string]any, error) {
	var schema FormSchema
	if err := json.Unmarshal(a.RequestedSchema, &schema); err != nil {
		return nil, err
	}
	content := map[string]any{}
	for key, value := range fields {
		field, ok := schema.Properties[key]
		if !ok {
			continue
		}
		if value == "" {
			continue
		}
		if len(field.Enum) > 0 || field.Type != "string" {
			var decoded any
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return nil, fmt.Errorf("Invalid value for %s", textutil.FirstNonEmpty(field.Title, key))
			}
			content[key] = decoded
		} else {
			content[key] = value
		}
	}
	if err := interaction.ValidateForm(a.RequestedSchema, content); err != nil {
		return nil, err
	}
	return content, nil
}

// LegacyResponse is the transport bridge for background services that predate
// normalized interaction responses. Presentation code never builds wire objects.
func LegacyResponse(a interaction.Request, reply interaction.Response) any {
	return legacy.EncodeResponse(a, reply)
}
