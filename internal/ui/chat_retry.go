package desktopui

import (
	"miya/internal/appmodel"
	"miya/internal/chat/model"
)

func (app *App) retryResponse(m model.Message) {
	req := appmodel.RetryRequest{ThreadID: app.C.Current().ID, MessageID: m.ID}
	if app.OnRetry != nil {
		app.OnRetry(req)
		return
	}
	if err := app.C.RetryResponse(req); err != nil {
		app.Notice = err.Error()
	}
}
