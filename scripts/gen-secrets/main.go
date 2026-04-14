// gen-secrets generates cryptographically secure random values for JWT secrets
// and the service token, printing them ready to paste into .env.
//
// Usage:
//
//	go run ./scripts/gen-secrets
package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
)

func main() {
	fmt.Println("# Paste these into your .env file:")
	fmt.Printf("JWT_ACCESS_SECRET=%s\n", mustSecret(32))
	fmt.Printf("JWT_REFRESH_SECRET=%s\n", mustSecret(32))
	fmt.Printf("SERVICE_TOKEN=%s\n", mustSecret(24))
}

// mustSecret returns n random bytes encoded as URL-safe base64 (no padding).
func mustSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("crypto/rand: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
