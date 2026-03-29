package utils

import (
	"regexp"
	"strings"
)

var (
	// phoneRe extracts a Colombian mobile: optional +57/57 prefix, then 3 + 9 digits
	phoneRe  = regexp.MustCompile(`(?:\+?57)?3\d{9}`)
	mobileRe = regexp.MustCompile(`^3\d{9}$`)
	digitRe  = regexp.MustCompile(`\d`)
)

// ParseColombianPhone parsea y normaliza un número de teléfono colombiano.
// Maneja múltiples números separados por espacio, coma, guion, punto, slash.
// Retorna formato "+57XXXXXXXXXX" o "" si no es válido.
func ParseColombianPhone(phoneString string) string {
	if phoneString == "" {
		return ""
	}

	lower := strings.ToLower(strings.TrimSpace(phoneString))
	if lower == "null" || lower == "no tiene" || lower == "n/a" || lower == "-" {
		return ""
	}

	// 1. Try to find a valid phone pattern directly (handles multi-number strings)
	if match := phoneRe.FindString(phoneString); match != "" {
		digits := digitRe.FindAllString(match, -1)
		all := strings.Join(digits, "")
		mobile := all[len(all)-10:]
		if mobileRe.MatchString(mobile) {
			return "+57" + mobile
		}
	}

	// 2. Fallback: strip ALL non-digits and try as single number
	//    Handles spaced formatting like "+57 300 123 4567" or "300-123-4567"
	allDigits := strings.Join(digitRe.FindAllString(phoneString, -1), "")

	// 10 digits starting with 3
	if len(allDigits) == 10 && mobileRe.MatchString(allDigits) {
		return "+57" + allDigits
	}

	// 12 digits starting with 57
	if len(allDigits) == 12 && strings.HasPrefix(allDigits, "57") {
		mobile := allDigits[2:]
		if mobileRe.MatchString(mobile) {
			return "+57" + mobile
		}
	}

	return ""
}

// FormatPhoneDisplay formatea un teléfono para mostrar al usuario.
// "+573103343616" → "+57 310 334 3616", vacío → "(no registrado)"
func FormatPhoneDisplay(phone string) string {
	parsed := ParseColombianPhone(phone)
	if parsed == "" {
		if phone == "" {
			return "(no registrado)"
		}
		return phone
	}
	d := parsed[3:] // quitar "+57"
	return "+57 " + d[:3] + " " + d[3:6] + " " + d[6:]
}
