package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestBackgroundTerminalsPaginationAndOpaqueIDs(t *testing.T) {
	client, server := testConnection(t)
	go func() {
		scanner := bufio.NewScanner(server)
		for i := 0; i < 2 && scanner.Scan(); i++ {
			var req Frame
			_ = json.Unmarshal(scanner.Bytes(), &req)
			var args struct{ ThreadID, Cursor string }
			_ = json.Unmarshal(req.Params, &args)
			if args.ThreadID != "thread" {
				t.Error("wrong thread queried")
			}
			if i == 0 {
				fmt.Fprintf(server, "{\"id\":%s,\"result\":{\"data\":[{\"processId\":\"1234\",\"itemId\":\"call\",\"osPid\":null,\"command\":\"node app\"}],\"nextCursor\":\"page2\"}}\n", req.ID)
			} else {
				if args.Cursor != "page2" {
					t.Error("cursor omitted")
				}
				fmt.Fprintf(server, "{\"id\":%s,\"result\":{\"data\":[{\"processId\":\"pty-2\",\"osPid\":777}],\"nextCursor\":null}}\n", req.ID)
			}
		}
	}()
	tasks, err := client.BackgroundTerminals(context.Background(), "thread")
	if err != nil || len(tasks) != 2 || tasks[0].OSPID != nil || tasks[0].ProcessID != "1234" || *tasks[1].OSPID != 777 {
		t.Fatalf("native terminal set: %+v %v", tasks, err)
	}
}
func TestBackgroundTerminalsDoesNotPublishPartialPages(t *testing.T) {
	client, server := testConnection(t)
	go func() {
		scanner := bufio.NewScanner(server)
		for i := 0; i < 2 && scanner.Scan(); i++ {
			var req Frame
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if i == 0 {
				fmt.Fprintf(server, "{\"id\":%s,\"result\":{\"data\":[{\"processId\":\"first\"}],\"nextCursor\":\"next\"}}\n", req.ID)
			} else {
				fmt.Fprintf(server, "{\"id\":%s,\"error\":{\"code\":-32601,\"message\":\"unavailable\"}}\n", req.ID)
			}
		}
	}()
	tasks, err := client.BackgroundTerminals(context.Background(), "thread")
	if err == nil || tasks != nil {
		t.Fatalf("partial set published: %+v %v", tasks, err)
	}
}

func TestMissingBackgroundSetIsNotKnownEmpty(t *testing.T) {
	client, server := testConnection(t)
	go func() {
		scanner := bufio.NewScanner(server)
		if scanner.Scan() {
			var req Frame
			_ = json.Unmarshal(scanner.Bytes(), &req)
			fmt.Fprintf(server, "{\"id\":%s,\"result\":{}}\n", req.ID)
		}
	}()
	tasks, err := client.BackgroundTerminals(context.Background(), "thread")
	if err == nil || tasks != nil {
		t.Fatal("missing native data became a known empty set")
	}
}
