package utils

import (
	"fmt"
	"time"
)

var dayNames = [7]string{"Dom", "Lun", "Mar", "Mié", "Jue", "Vie", "Sáb"}

var monthNames = [12]string{
	"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio",
	"Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre",
}

// FormatFriendlyDateShort formatea una fecha como "Lun 15/03"
func FormatFriendlyDateShort(t time.Time) string {
	return fmt.Sprintf("%s %02d/%02d", dayNames[t.Weekday()], t.Day(), t.Month())
}

// FormatFriendlyDateShortStr formatea una fecha string YYYY-MM-DD como "Lun 15/03"
func FormatFriendlyDateShortStr(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	return FormatFriendlyDateShort(t)
}

// FormatFriendlyDate formatea una fecha como "Lunes 15 de Marzo de 2026"
func FormatFriendlyDate(t time.Time) string {
	dayFull := [7]string{"Domingo", "Lunes", "Martes", "Miércoles", "Jueves", "Viernes", "Sábado"}
	return fmt.Sprintf("%s %d de %s de %d", dayFull[t.Weekday()], t.Day(), monthNames[t.Month()-1], t.Year())
}

// FormatFriendlyDateStr formatea una fecha string YYYY-MM-DD como "Lunes 15 de Marzo de 2026"
func FormatFriendlyDateStr(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	return FormatFriendlyDate(t)
}
