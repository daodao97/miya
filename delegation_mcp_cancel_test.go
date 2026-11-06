package main

import (
	json "encoding/json"
	fmt "fmt"
	io "io"
	appmodel "miya/internal/appmodel"
	strings "strings"
	sync "sync"
	atomic "sync/atomic"
	testing "testing"
	time "time"
)

type mcpFrameWriter chan []byte

func (w mcpFrameWriter) Write(data []byte) (int, error) {
	w <- append([]byte(nil), data...)
	return len(data), nil
}

func TestMCPWaitCancellationAndEOFReleaseObserverWithoutCancellingTask(t *testing.T) {
	for _, mode := range []string{"notification", "eof"} {
		t.Run(mode, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			frames := make(mcpFrameWriter, 4)
			queried := make(chan struct{})
			var once sync.Once
			var reads, cancels atomic.Int32
			call := func(op string, args, out any) error {
				r := args.(appmodel.McpRequest)
				if r.Tool == "cancel_task" {
					cancels.Add(1)
				}
				if op != "mcpCollaboration" || r.Tool != "wait_task" {
					return fmt.Errorf("unexpected call %s %s", op, r.Tool)
				}
				reads.Add(1)
				*out.(*json.RawMessage) = json.RawMessage(`{"task_id":"task-live","state":"running","revision":1}`)
				once.Do(func() { close(queried) })
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- appmodel.ServeMCP(reader, frames, "/workspace", call, time.Sleep) }()
			if _, err := io.WriteString(writer, "{\"jsonrpc\":\"2.0\",\"id\":17,\"method\":\"tools/call\",\"params\":{\"name\":\"wait_task\",\"arguments\":{\"task_id\":\"task-live\",\"wait_seconds\":120}}}\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-queried:
			case <-time.After(time.Second):
				t.Fatal("wait did not begin")
			}
			if mode == "notification" {
				if _, err := io.WriteString(writer, "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/cancelled\",\"params\":{\"requestId\":17}}\n"); err != nil {
					t.Fatal(err)
				}
			} else {
				writer.Close()
			}
			select {
			case data := <-frames:
				var response map[string]any
				if err := json.Unmarshal(data, &response); err != nil {
					t.Fatal(err)
				}
				text, failed := mcpText(t, response)
				if !failed || !strings.Contains(text, "canceled") {
					t.Fatalf("cancellation response: %s", data)
				}
			case <-time.After(time.Second):
				t.Fatal("observer outlived cancellation")
			}
			writer.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("MCP EOF waited for the task's 120-second deadline")
			}
			if reads.Load() < 1 || cancels.Load() != 0 {
				t.Fatalf("observer cancellation affected native work or continued polling: reads=%d cancels=%d", reads.Load(), cancels.Load())
			}
		})
	}
}
