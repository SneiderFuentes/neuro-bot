package services

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

type AppointmentService struct {
	repo repository.AppointmentRepository
}

func NewAppointmentService(repo repository.AppointmentRepository) *AppointmentService {
	return &AppointmentService{repo: repo}
}

// GetUpcomingAppointments retorna las citas futuras no canceladas del paciente
func (s *AppointmentService) GetUpcomingAppointments(ctx context.Context, patientID string) ([]domain.Appointment, error) {
	return s.repo.FindUpcomingByPatient(ctx, patientID)
}

// FindConsecutiveBlock detecta un bloque de citas consecutivas a partir de una cita.
// Regla: mismo paciente + mismo doctor + mismo día + misma agenda + horas consecutivas.
// Recibe la lista completa de citas del paciente (ya obtenida) para evitar otra query.
func (s *AppointmentService) FindConsecutiveBlock(appointments []domain.Appointment, mainApptID string) []domain.Appointment {
	var mainAppt *domain.Appointment
	for i, a := range appointments {
		if a.ID == mainApptID {
			mainAppt = &appointments[i]
			break
		}
	}
	if mainAppt == nil {
		return nil
	}

	// Filtrar por mismo día + mismo doctor + misma agenda
	dateStr := mainAppt.Date.Format("2006-01-02")
	var sameDayGroup []domain.Appointment
	for _, a := range appointments {
		if a.Date.Format("2006-01-02") == dateStr &&
			a.DoctorID == mainAppt.DoctorID &&
			a.AgendaID == mainAppt.AgendaID {
			sameDayGroup = append(sameDayGroup, a)
		}
	}

	if len(sameDayGroup) <= 1 {
		return sameDayGroup
	}

	// Ordenar por timeslot
	sort.Slice(sameDayGroup, func(i, j int) bool {
		return sameDayGroup[i].TimeSlot < sameDayGroup[j].TimeSlot
	})

	// Inferir duración desde el gap entre los dos primeros
	gap := ParseTimeSlotToMinutes(sameDayGroup[1].TimeSlot) - ParseTimeSlotToMinutes(sameDayGroup[0].TimeSlot)
	if gap <= 0 {
		return []domain.Appointment{*mainAppt}
	}

	// Construir sub-bloques con gap consistente
	var blocks [][]domain.Appointment
	current := []domain.Appointment{sameDayGroup[0]}

	for i := 1; i < len(sameDayGroup); i++ {
		diff := ParseTimeSlotToMinutes(sameDayGroup[i].TimeSlot) - ParseTimeSlotToMinutes(sameDayGroup[i-1].TimeSlot)
		if diff == gap {
			current = append(current, sameDayGroup[i])
		} else {
			blocks = append(blocks, current)
			current = []domain.Appointment{sameDayGroup[i]}
		}
	}
	blocks = append(blocks, current)

	// Encontrar el bloque que contiene la cita principal
	for _, block := range blocks {
		for _, a := range block {
			if a.ID == mainApptID {
				return block
			}
		}
	}

	return []domain.Appointment{*mainAppt}
}

// ConfirmBlock confirma todas las citas del bloque atómicamente
func (s *AppointmentService) ConfirmBlock(ctx context.Context, block []domain.Appointment, channel, channelID string) error {
	ids := make([]string, len(block))
	for i, appt := range block {
		ids[i] = appt.ID
	}
	return s.repo.ConfirmBatch(ctx, ids, channel, channelID)
}

// CancelBlock cancela todas las citas del bloque atómicamente
func (s *AppointmentService) CancelBlock(ctx context.Context, block []domain.Appointment, reason, channel, channelID string) error {
	ids := make([]string, len(block))
	for i, appt := range block {
		ids[i] = appt.ID
	}
	return s.repo.CancelBatch(ctx, ids, reason, channel, channelID)
}

// ParseTimeSlotToMinutes convierte "YYYYMMDD0730" → 450 (7*60+30)
func ParseTimeSlotToMinutes(timeSlot string) int {
	if len(timeSlot) < 12 {
		return 0
	}
	hour, _ := strconv.Atoi(timeSlot[8:10])
	minute, _ := strconv.Atoi(timeSlot[10:12])
	return hour*60 + minute
}

// FormatTimeSlot convierte "YYYYMMDD0730" → "7:30 AM"
func FormatTimeSlot(timeSlot string) string {
	if len(timeSlot) < 12 {
		return "Hora no disponible"
	}
	hour, _ := strconv.Atoi(timeSlot[8:10])
	minute, _ := strconv.Atoi(timeSlot[10:12])

	ampm := "AM"
	displayHour := hour
	if hour >= 12 {
		ampm = "PM"
		if hour > 12 {
			displayHour = hour - 12
		}
	}
	if hour == 0 {
		displayHour = 12
	}

	return fmt.Sprintf("%d:%02d %s", displayHour, minute, ampm)
}

// --- Validaciones Médicas (Fase 9) ---

// CUPS que requieren consulta previa con doctor específico
var cupsRequiresPreviousDoctor = map[string][]string{
	"053105": {"890374", "890274"}, // Requiere cita previa de tipo 890374 o 890274
	"861402": {"890264", "890364"}, // Requiere cita previa de tipo 890264 o 890364
}

// SOAT group limits (máximo mensual por grupo, solo para SAN01/SAN02)
// Per R-PROC-09 in 02-BUSINESS-RULES.md
var soatGroups = map[string]struct {
	MaxPerMonth int
	CupsCodes   []string
}{
	"consulta_neurologia":       {MaxPerMonth: 397, CupsCodes: []string{"890274", "890374"}},
	"electroencefalograma":      {MaxPerMonth: 172, CupsCodes: []string{"891402", "891901", "891402-1", "891402PED", "891901-1", "891901PED", "891401", "891401PED"}},
	"bloqueos":                  {MaxPerMonth: 67, CupsCodes: []string{"053106", "053105", "053111"}},
	"aplicacion_sustancia":      {MaxPerMonth: 20, CupsCodes: []string{"861411", "48201"}},
	"polisomnografia":           {MaxPerMonth: 57, CupsCodes: []string{"891704", "891703", "891704-1", "891704PED", "891703-1", "891703PED"}},
	"otros_procedimientos":      {MaxPerMonth: 932, CupsCodes: []string{"891515", "891514", "930820", "891511", "891509", "930860", "891530", "952303", "954626", "952302", "930103", "930821", "954624", "954625", "952301", "930801", "891503", "891508"}},
}

// Restricciones de edad por doctor (hardcoded por negocio)
var doctorAgeRestrictions = map[string]struct {
	MinAge int
	Reason string
}{
	"74372158": {MinAge: 5, Reason: "Este doctor solo atiende pacientes mayores de 5 años"},
	"7178922":  {MinAge: 18, Reason: "Este doctor solo atiende pacientes mayores de 18 años"},
}

// CheckPriorConsultation verifica si el CUPS requiere consulta previa con el mismo doctor.
// Retorna (blocked, message, error).
func (s *AppointmentService) CheckPriorConsultation(ctx context.Context, cupsCode, patientID string) (bool, string, error) {
	requiredCups, exists := cupsRequiresPreviousDoctor[cupsCode]
	if !exists {
		return false, "", nil
	}

	for _, reqCup := range requiredCups {
		hasCup, err := s.repo.HasFutureForCup(ctx, patientID, reqCup)
		if err != nil {
			return false, "", err
		}
		if hasCup {
			return false, "", nil // Tiene consulta previa → OK
		}
	}

	return true, "Este procedimiento requiere una *consulta previa* con el especialista. Por favor agenda primero la consulta y luego el examen.", nil
}

// CheckSOATLimit verifica si el grupo CUPS ha alcanzado el límite mensual.
// Solo aplica para entidades SAN01 y SAN02.
func (s *AppointmentService) CheckSOATLimit(ctx context.Context, cupsCode, entity string) (bool, string, error) {
	if entity != "SAN01" && entity != "SAN02" {
		return false, "", nil
	}

	var groupName string
	var maxPerMonth int
	for name, group := range soatGroups {
		for _, code := range group.CupsCodes {
			if code == cupsCode {
				groupName = name
				maxPerMonth = group.MaxPerMonth
				break
			}
		}
		if groupName != "" {
			break
		}
	}

	if groupName == "" {
		return false, "", nil
	}

	count, err := s.repo.CountMonthlyByGroup(ctx, soatGroups[groupName].CupsCodes)
	if err != nil {
		return false, "", err
	}

	if count >= maxPerMonth {
		return true, fmt.Sprintf("Se ha alcanzado el límite mensual de %d citas para %s (SOAT). Por favor contacta a la clínica.", maxPerMonth, groupName), nil
	}

	return false, "", nil
}

// HasExistingAppointment verifica si el paciente ya tiene una cita futura para el CUPS.
func (s *AppointmentService) HasExistingAppointment(ctx context.Context, patientID, cupCode string) (bool, error) {
	return s.repo.HasFutureForCup(ctx, patientID, cupCode)
}

// GetDoctorAgeRestriction retorna la restricción de edad para un doctor, si existe.
func GetDoctorAgeRestriction(doctorDoc string) (minAge int, reason string, exists bool) {
	r, ok := doctorAgeRestrictions[doctorDoc]
	if !ok {
		return 0, "", false
	}
	return r.MinAge, r.Reason, true
}

// CreateWithConsecutive creates N consecutive appointments.
// For consecutive blocks, pxcita is only inserted on the first appointment.
// Duration is the slot length in minutes (from ScheduleConfig.AppointmentDuration).
func (s *AppointmentService) CreateWithConsecutive(ctx context.Context, input domain.CreateAppointmentInput, espacios, durationMinutes int) (string, error) {
	if espacios <= 1 || durationMinutes <= 0 {
		appt, err := s.repo.Create(ctx, input)
		if err != nil {
			return "", err
		}
		return appt.ID, nil
	}

	baseMinutes := ParseTimeSlotToMinutes(input.TimeSlot)
	dateStr := input.TimeSlot[:8] // YYYYMMDD

	var firstID string
	for i := 0; i < espacios; i++ {
		minutes := baseMinutes + (i * durationMinutes)
		if minutes/60 >= 24 {
			return "", fmt.Errorf("consecutive slot %d/%d exceeds 24h (minute %d)", i+1, espacios, minutes)
		}
		timeSlot := fmt.Sprintf("%s%02d%02d", dateStr, minutes/60, minutes%60)

		consecutiveInput := input
		consecutiveInput.TimeSlot = timeSlot

		// Only include procedures in the first appointment
		if i > 0 {
			consecutiveInput.Procedures = nil
		}

		appt, err := s.repo.Create(ctx, consecutiveInput)
		if err != nil {
			return "", fmt.Errorf("create consecutive %d/%d: %w", i+1, espacios, err)
		}

		if i == 0 {
			firstID = appt.ID
		}
	}

	return firstID, nil
}

// FindBlockByAppointmentID fetches the full consecutive block for an appointment.
func (s *AppointmentService) FindBlockByAppointmentID(ctx context.Context, apptID string) (*domain.Appointment, []domain.Appointment, error) {
	appt, err := s.repo.FindByID(ctx, apptID)
	if err != nil || appt == nil {
		return nil, nil, err
	}

	dateStr := appt.Date.Format("2006-01-02")
	dayAppts, err := s.repo.FindByAgendaAndDate(ctx, appt.AgendaID, dateStr)
	if err != nil {
		return appt, []domain.Appointment{*appt}, nil // Fallback: single appointment
	}

	block := s.FindConsecutiveBlock(dayAppts, apptID)
	if len(block) == 0 {
		block = []domain.Appointment{*appt}
	}

	return appt, block, nil
}

// GetFirstCupName retorna el nombre del primer procedimiento de una cita
func GetFirstCupName(appt domain.Appointment) string {
	if len(appt.Procedures) > 0 && appt.Procedures[0].CupName != "" {
		return appt.Procedures[0].CupName
	}
	if len(appt.Procedures) > 0 {
		return appt.Procedures[0].CupCode
	}
	return "Procedimiento"
}
