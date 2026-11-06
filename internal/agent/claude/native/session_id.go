package claude

import "miya/internal/textutil"

func NewSessionID() string { return textutil.NewID() }
