package agent

import "miya/internal/agent/account"

// AccountProvider is optional: integrations may expose account controls
// independently of conversation execution and subscription polling.
type AccountProvider interface{ Account() account.Source }
