package control

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopWakesDisplayOnStartAndWhileCapturing(t *testing.T) {
	d, token, _ := mockDesktop(t)
	var wakes atomic.Int32
	d.Wake = func() error { wakes.Add(1); return nil }
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if wakes.Load() != 0 {
		t.Fatal("woke the display before consent")
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if wakes.Load() != 1 {
		t.Fatal("start did not wake the display", wakes.Load())
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if wakes.Load() != 1 {
		t.Fatal("frame refreshed activity within the interval")
	}
	d.mu.Lock()
	d.lease.lastWake = time.Now().Add(-6 * time.Second)
	d.mu.Unlock()
	time.Sleep(260 * time.Millisecond)
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if wakes.Load() != 2 {
		t.Fatal("polling did not keep the display awake", wakes.Load())
	}
	d.Revoke(token)
	desktopHTTP(d, token, "GET", "/frame", "")
	if wakes.Load() != 2 {
		t.Fatal("woke the display after control ended")
	}
}

func TestDesktopCaptureFailureWakesDisplayAndRetries(t *testing.T) {
	d, token, _ := mockDesktop(t)
	var wakes, captures atomic.Int32
	d.Wake = func() error { wakes.Add(1); return nil }
	d.Capture = func() ([]byte, error) {
		if captures.Add(1) == 1 {
			return nil, errors.New("display asleep")
		}
		return []byte("jpeg"), nil
	}
	if w := desktopHTTP(d, token, "POST", "/start", `{}`); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w := desktopHTTP(d, token, "GET", "/frame", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if captures.Load() != 2 || wakes.Load() != 2 {
		t.Fatal("expected one wake-and-retry", captures.Load(), wakes.Load())
	}
}
