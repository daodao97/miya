package desktopui

import (
	appmodel "miya/internal/appmodel"
)

func (app *App) retryResponse(m appmodel.Message) {
	req := appmodel.RetryRequest{ThreadID: app.C.Current().ID, MessageID: m.ID}
	if app.OnRetry != nil {
		app.OnRetry(req)
		return
	}
	if err := app.C.RetryResponse(req); err != nil {
		app.Notice = err.Error()
	}
}
