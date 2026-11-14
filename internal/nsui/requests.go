package nsui

import (
	"github.com/nsgo-dev/nsgo/core"
	"miya/internal/interaction"
	"miya/internal/nsrequests"
)

type requestsHost = nsrequests.Host

func pendingRequests(cx *core.Scope, c requestsHost, approvals func() []interaction.Request) core.Node {
	return nsrequests.Pending(cx, c, approvals)
}
func displayIf(on bool) core.Display {
	if on {
		return core.DisplayFlex
	}
	return core.DisplayNone
}
