package model

import (
	"encoding/json"
	"testing"
)

func TestActivityRetainsUnknownCachedFields(t *testing.T) {
	var thread Thread
	const cache = `{"Messages":[{"Role":"tool","Item":{"server":"external","tool":"inspect","future":{"revision":17}}}]}`
	if err := json.Unmarshal([]byte(cache), &thread); err != nil {
		t.Fatal(err)
	}
	if len(thread.Messages) != 1 || thread.Messages[0].Item.Tool != "inspect" {
		t.Fatal("cached tool lost")
	}
	data, err := json.Marshal(thread)
	if err != nil {
		t.Fatal(err)
	}
	var restored struct {
		Messages []struct {
			Item struct {
				Future struct {
					Revision int `json:"revision"`
				} `json:"future"`
			}
		}
	}
	if err := json.Unmarshal(data, &restored); err != nil || len(restored.Messages) != 1 || restored.Messages[0].Item.Future.Revision != 17 {
		t.Fatalf("unknown cache fields were erased: %s, %v", data, err)
	}
}
