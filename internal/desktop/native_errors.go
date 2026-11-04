package desktop

import (
	"errors"
	"fmt"
)

// Native bridge statuses distinguish unsupported systems from failures on a
// supported Mac. In particular, a failed screenshot must not suggest granting
// screen recording again when permission was already accepted.
func permissionError(code int) error {
	switch code {
	case 0:
		return nil
	case 1:
		return errors.New("请在 Mac 的「系统设置 → 隐私与安全性 → 屏幕录制」中允许 Miya，然后重试；系统可能要求重开应用")
	case 2:
		return errors.New("请在 Mac 的「系统设置 → 隐私与安全性 → 辅助功能」中允许 Miya，然后重试")
	case 3:
		return errors.New("远程桌面需要 macOS 14 或更新版本")
	case 4:
		return ErrWindowUnavailable
	case 5:
		return errors.New("系统暂时无法获取可共享窗口；Mac 可能正在唤醒，请稍候重试。若持续失败，请确认 Mac 已登录")
	case 6:
		return errors.New("目标窗口不在系统可共享列表中；请在 Mac 上完成此步骤")
	case 7:
		return errors.New("系统未能生成目标画面；可重试，若持续失败请在 Mac 上完成此步骤")
	case 8:
		return errors.New("获取画面超时；Mac 可能正在唤醒，请稍候重试。若持续失败，请确认 Mac 在线并已登录")
	case 9:
		return errors.New("画面编码失败，请重试")
	case 10:
		return errors.New("画面超过大小限制，请重试")
	case 11:
		return errors.New("无法分配画面内存，请稍后重试")
	case 13:
		return errors.New("Mac 屏幕暂不可用，可能处于休眠；正在唤醒，请稍后重试")
	case 12:
		return errors.New("无法唤醒 Mac 显示器，请在 Mac 上唤醒后重试")
	default:
		return fmt.Errorf("远程操作失败（错误码 %d），请重试", code)
	}
}

func captureError(code int, diagnostic string) error {
	err := permissionError(code)
	if err == nil || diagnostic == "" {
		return err
	}
	return fmt.Errorf("%w（%s）", err, diagnostic)
}
