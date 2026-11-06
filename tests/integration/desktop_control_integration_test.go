//go:build darwin && !ios && cgo

package integration

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestMain(m *testing.M) {
	if os.Getenv("MIYA_DESKTOP_QA_DIR") == "" {
		os.Exit(m.Run())
	}
	_ = flag.Set("test.run", "^TestDesktopNativeQA$")
	_ = flag.Set("test.v", "true")
	mygo.App.WhenReady(func() {
		var scratch string
		mygo.NewWindow(mygo.WindowOptions{Title: "Miya Desktop QA · 输入验证", Width: 600, Height: 400, Content: ui.View(func(c *ui.Context) {
			ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
				ui.Text(c, "远程桌面输入验证").FontSize(22)
				ui.TextArea(c, &scratch).Grow(1).FillWidth().Label("QA 输入区域").Placeholder("只在此区域验证输入")
			})
		})})
		go func() { os.Exit(m.Run()) }()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
