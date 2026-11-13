package desktopui

import (
	"github.com/egoist/mygo"

	"miya/internal/uiruntime"
)

// MyGo provides the platform services the controller reaches through
// uiruntime. main imports this package on every platform, phones included.
func init() {
	uiruntime.IdleTime = mygo.Power.IdleTime
	uiruntime.ShowNotice = func(n uiruntime.Notice) error {
		notice := mygo.NewNotification(mygo.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body, Group: n.Group, Data: n.Data})
		if n.OnClick != nil {
			notice.OnClick(n.OnClick)
		}
		if err := notice.Show(); err != nil {
			notice.Close()
			return err
		}
		return nil
	}
}

// mainWindow is the app's window as MyGo's, for what only MyGo's window
// offers: web views, dialogs' parents, whether it was destroyed.
func mainWindow() *mygo.Window {
	w, _ := uiruntime.CurrentWindow().(*mygo.Window)
	return w
}
