package util

import (
	"strings"
	"unicode"
)

// IsValidSupi validates the format of a SUPI (Subscription Permanent Identifier)
func IsValidSupi(supi string) bool {
	switch {
	case strings.HasPrefix(supi, "imsi-"):
		return isValidIMSI(supi)

	case strings.HasPrefix(supi, "nai-"):
		return isValidNAI(supi)

	default:
		return false
	}
}

// IMSI: "imsi-" + 5~15 digits
func isValidIMSI(supi string) bool {
	const prefix = "imsi-"
	const minDigits = 5
	const maxDigits = 15

	digits := strings.TrimPrefix(supi, prefix)
	if len(digits) < minDigits || len(digits) > maxDigits {
		return false
	}

	for _, r := range digits {
		if !unicode.IsDigit(r) {
			return false
		}
	}

	return true
}

// NAI: "nai-" + content
func isValidNAI(supi string) bool {
	const prefix = "nai-"

	content := strings.TrimPrefix(supi, prefix)
	return len(content) > 0
}
