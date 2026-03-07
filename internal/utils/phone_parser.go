package utils

import (
	"regexp"
	"strings"
)

var (
	nonDigitPlusRe = regexp.MustCompile(`[^\d+\-/]`)
	separatorRe    = regexp.MustCompile(`[-/]`)
	nonDigitRe     = regexp.MustCompile(`[^\d]`)
	mobileRe       = regexp.MustCompile(`^3\d{9}$`)
)

// ParseColombianPhone parsea y normaliza un número de teléfono colombiano.
// Retorna formato "+57XXXXXXXXXX" o "" si no es válido.
func ParseColombianPhone(phoneString string) string {
	if phoneString == "" {
		return ""
	}

	lower := strings.ToLower(strings.TrimSpace(phoneString))
	if lower == "null" || lower == "no tiene" || lower == "n/a" || lower == "-" {
		return ""
	}

	cleaned := nonDigitPlusRe.ReplaceAllString(phoneString, "")
	numbers := separatorRe.Split(cleaned, -1)

	for _, number := range numbers {
		digits := nonDigitRe.ReplaceAllString(number, "")

		// +57XXXXXXXXXX (12+ dígitos con prefijo 57)
		if strings.Contains(cleaned, "+57") && len(digits) >= 12 {
			mobile := digits[len(digits)-10:]
			if mobileRe.MatchString(mobile) {
				return "+57" + mobile
			}
		}

		// 10 dígitos empezando con 3
		if len(digits) == 10 && mobileRe.MatchString(digits) {
			return "+57" + digits
		}

		// 12 dígitos empezando con 57
		if len(digits) == 12 && strings.HasPrefix(digits, "57") {
			mobile := digits[2:]
			if mobileRe.MatchString(mobile) {
				return "+57" + mobile
			}
		}
	}

	return ""
}
