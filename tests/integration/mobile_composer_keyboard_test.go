//go:build !windows

package integration

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

// The keyboard rising must not give the composer another identity, even for
// a frame: the focus would go, the keyboard close, and come back, in a loop
// that flickers on a phone. In a conversation, what the bar shows above the
// composer comes and goes as the keyboard moves the messages.
func TestComposerKeepsFocusAsKeyboardRises(t *testing.T) {
	for _, n := range []int{0, 2, 30} {
		m := testMobileApp()
		th := m.State.Projects[0].Threads[0]
		for i := range n {
			th.Messages = append(th.Messages, model.Message{ID: i + 1, Content: fmt.Sprintf("message %d with some text to wrap around the bubble", i)})
		}
		m.State.Projects[0].Threads[0] = th
		m.OpenThread(th)
		m.ShowThread(th)
		m.Viewer.SetThreads([]model.Thread{th})
		m.Viewer.SetActive(th.ID)
		tt := ui.NewTester(m.ViewRoot, 390, 844)
		setSafeInsets(tt, safeInsets{Top: 59, Bottom: 34})
		tt.Frame()
		b, ok := tt.Find("Message")
		if !ok {
			t.Fatalf("%d messages: no composer", n)
		}
		tt.FingerDown(b.X+b.W/2, b.Y+b.H/2)
		tt.FingerUp(b.X+b.W/2, b.Y+b.H/2)
		tt.Frame()
		for step, bottom := range []float32{374, 374, 34, 374} {
			setSafeInsets(tt, safeInsets{Top: 59, Bottom: bottom})
			if _, active := tt.TextCaret(); !active || !tt.Focused("Message") {
				t.Fatalf("%d messages, step %d (inset %v): focus %v, text input %v", n, step, bottom, tt.Focused("Message"), active)
			}
		}
	}
}
