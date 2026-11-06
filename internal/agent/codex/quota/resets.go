package quota

import (
	"context"
	"errors"
	"time"

	"miya/internal/agent/quota"
)

func useCodexReset(ctx context.Context, binary, home string, attempt quota.ResetAttempt) (quota.ResetResult, error) {
	if attempt.Key == "" || attempt.AccountKey == "" || attempt.Home != home {
		return quota.ResetResult{}, errors.New("Account confirmation missing. Refresh and retry.")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out quota.ResetResult
	err := withCodex(ctx, binary, home, func(rpc *codexRPC) error {
		current, err := rpc.read()
		if err != nil {
			return err
		}
		if current.AccountKey != attempt.AccountKey {
			return errors.New("The Codex account changed. No reset card used. Refresh and confirm again.")
		}
		params := map[string]string{"idempotencyKey": attempt.Key}
		if attempt.CreditID != "" {
			params["creditId"] = attempt.CreditID
		}
		// Send exactly once. Even a zero count may mean an earlier, timed-out
		// use succeeded: its same idempotency key resolves that outcome safely.
		if err := rpc.call("account/rateLimitResetCredit/consume", params, &out); err != nil {
			return errors.New("Reset result unconfirmed. Refresh or retry the same operation. Older CLI versions may not support reset cards.")
		}
		switch out.Outcome {
		case "reset", "alreadyRedeemed", "nothingToReset", "noCredit":
		default:
			return errors.New("Unable to confirm reset result. Refresh or retry the same operation.")
		}
		out.Reading, err = rpc.read()
		if err != nil {
			out.Reading = quota.Reading{Agent: "codex", At: time.Now(), Error: "Reset confirmed, but quota could not be read. Refresh to check."}
		}
		return nil
	})
	if err != nil && ctx.Err() != nil {
		err = errors.New("Reset request timed out; result unconfirmed. Refresh or retry the same operation.")
	}
	return out, err
}
