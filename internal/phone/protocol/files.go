package protocol

import (
	"crypto/sha256"
	"encoding/hex"
)

// ProjectFileID is the stable project prefix used by both file servers and proxies.
func ProjectFileID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:16])
}
