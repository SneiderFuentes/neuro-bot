package validators

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/utils"
)

var (
	nameRegex  = regexp.MustCompile(`^[a-zA-ZáéíóúñÁÉÍÓÚÑüÜ\s]{2,50}$`)
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
)

// Name valida un nombre (letras + espacios, 2-50 chars).
func Name(s string) bool {
	return nameRegex.MatchString(s)
}

// NotEmpty valida que el string no sea vacío después de trim.
func NotEmpty(s string) bool {
	return len(strings.TrimSpace(s)) > 0
}

// Document valida un número de documento (5-15 dígitos).
func Document(s string) bool {
	if len(s) < 5 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Email valida un email estándar.
func Email(s string) bool {
	return emailRegex.MatchString(strings.ToLower(s))
}

// ColombianPhone valida un teléfono celular colombiano.
func ColombianPhone(s string) bool {
	return utils.ParseColombianPhone(s) != ""
}

// MinLength retorna un validador que exige al menos n caracteres.
func MinLength(n int) func(string) bool {
	return func(s string) bool {
		return len(strings.TrimSpace(s)) >= n
	}
}

// NumRange retorna un validador para enteros en [min, max].
func NumRange(min, max int) func(string) bool {
	return func(s string) bool {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return false
		}
		return n >= min && n <= max
	}
}

// FloatRange retorna un validador para floats en [min, max].
// Acepta tanto punto como coma decimal (ej: "70.5" o "70,5").
func FloatRange(min, max float64) func(string) bool {
	return func(s string) bool {
		s = strings.Replace(strings.TrimSpace(s), ",", ".", 1)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return false
		}
		return f >= min && f <= max
	}
}
