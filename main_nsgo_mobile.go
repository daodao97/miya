//go:build nsgo && (ios || android)

package main

import "miya/internal/nsmobile"

func main() { nsmobile.MobileEntry() }
