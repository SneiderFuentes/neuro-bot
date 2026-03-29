package utils

// maskPhones controls whether phone numbers are masked in logs.
// Default: true (production). Set to false via LOG_MASK_PHONES=false for debugging.
var maskPhones = true

// SetMaskPhones configures whether MaskPhone masks or passes through.
func SetMaskPhones(mask bool) {
	maskPhones = mask
}

// MaskPhone masks a phone number for logging: "+573103343616" → "+573***3616".
// When masking is disabled (LOG_MASK_PHONES=false), returns the phone unchanged.
func MaskPhone(phone string) string {
	if !maskPhones {
		return phone
	}
	if len(phone) < 7 {
		return "***"
	}
	return phone[:4] + "***" + phone[len(phone)-4:]
}
