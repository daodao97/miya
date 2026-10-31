package remote

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestConnCompression(t *testing.T) {
	a, b := net.Pipe()
	desk, phone := newConn(a, nil), newConn(b, nil)
	defer desk.Close()
	defer phone.Close()
	big := strings.Repeat("## Heading\n\nSome markdown text that repeats, as tool output does.\n", 400)
	data, _ := json.Marshal(big)
	errs := make(chan error, 1)
	go func() {
		err := desk.Send(Frame{Type: "hello", Version: ProtocolVersion})
		if err == nil {
			err = desk.CompressSends()
		}
		for i := 0; i < 3 && err == nil; i++ {
			err = desk.Send(Frame{Type: "thread", Thread: i, Data: data})
		}
		if err != nil {
			errs <- err
			desk.Close()
		}
	}()
	var hello Frame
	if err := phone.Receive(&hello); err != nil || hello.Type != "hello" {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	phone.DecompressReceives()
	for i := 0; i < 3; i++ {
		var f Frame
		if err := phone.Receive(&f); err != nil || f.Thread != i || !strings.Contains(string(f.Data), "Heading") {
			select {
			case e := <-errs:
				t.Fatalf("send: %v", e)
			default:
			}
			t.Fatalf("frame %d = %v, %v", i, f.Type, err)
		}
	}
	raw := 3 * len(big)
	if w := desk.Written(); w*4 > int64(raw) {
		t.Errorf("sent %d bytes for %d of text; compression should shrink it well", w, raw)
	} else {
		t.Logf("sent %d bytes for %d bytes of text", w, raw)
	}
}
