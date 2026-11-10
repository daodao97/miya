package integration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"miya/internal/chat/model"
)

const mobileLongPrompt = "🔴 [prod] 📍 [6b592b9c36fc] ⚠️ 计费异常：响应体解析失败，未扣费\n\n📊 查看监控: https://log.aicoding.inc/select/vmui/#/?g0.range_input=30d&g0.relative_time=last_30_days&query=trace_id%3A%3D%226af2176e-d7cb-4c46-b4e5-1e85df4465d1%22&step=1d&view=group\n@大助手|aicoding.sh(大助手|aicoding.sh) relay中的报错，看看那个渠道什么问题"

func TestMobileLongUserMessageFitsScreen(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := testMobileApp()
			th := m.State.Projects[0].Threads[0]
			th.HistoryLoaded = true
			th.Messages = []model.Message{{ID: 1, Role: "user", Content: mobileLongPrompt}}
			m.OpenThread(th)
			m.ShowThread(th)
			tt := ui.NewTester(m.ViewRoot, width, 844)
			setSafeInsets(tt, safeInsets{Top: 59, Bottom: 34})
			tt.Frame()
			var labels []string
			for _, text := range tt.Texts() {
				if strings.Contains(text, "计费异常") || strings.Contains(text, "查看监控") || strings.Contains(text, "relay中的报错") {
					labels = append(labels, text)
				}
			}
			if len(labels) == 0 {
				t.Fatal("long user message did not render")
			}
			for _, label := range labels {
				r, ok := tt.Find(label)
				if !ok || r.X < 16 || r.X+r.W > float32(width)-16 {
					t.Errorf("message text %q extends outside the conversation: %v", label, r)
				}
			}
			saveMobileNativeSnapshot(t, tt, fmt.Sprintf("long-message-%d", width))
		})
	}
}

func TestMobileLongDraftPaintStaysInsideEditor(t *testing.T) {
	m := testMobileApp()
	th := m.State.Projects[0].Threads[0]
	th.HistoryLoaded = true
	m.OpenThread(th)
	m.ShowThread(th)
	m.Draft = strings.Repeat(mobileLongPrompt+"\n", 3)
	tt := ui.NewTester(m.ViewRoot, 390, 510) // The viewport above the keyboard.
	tt.SetDark(true)
	setSafeInsets(tt, safeInsets{Top: 59})
	tt.Frame()
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyEnd)
	tt.Frame()
	input, _ := tt.Find("Message")
	header, _ := tt.Find("Codex · proj-a")
	saveMobileNativeSnapshot(t, tt, "long-draft-keyboard")
	img := tt.Image()
	for y := int(header.Y+header.H) + 16; y < int(input.Y)-8; y++ {
		for x := 30; x < 360; x++ {
			p := img.RGBAAt(x, y)
			if p.R > 80 || p.G > 80 || p.B > 80 {
				t.Fatalf("draft painted outside the editor at (%d, %d), input=%v", x, y, input)
			}
		}
	}
	caret, active := tt.TextCaret()
	if !active || caret.Y < input.Y || caret.Y+caret.H > input.Y+input.H+1 {
		t.Fatalf("long draft caret is not visible: caret=%v input=%v", caret, input)
	}
}
