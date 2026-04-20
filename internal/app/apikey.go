package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const apiKeyPrefix = "sk-ant-api01-"

func GenerateAPIKey() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return apiKeyPrefix + hex.EncodeToString(b)
}

func IsValidAPIKeyFormat(key string) bool {
	return len(key) > len(apiKeyPrefix) && key[:len(apiKeyPrefix)] == apiKeyPrefix
}
