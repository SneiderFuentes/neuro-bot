package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

type PatientService struct {
	repo repository.PatientRepository
}

func NewPatientService(repo repository.PatientRepository) *PatientService {
	return &PatientService{repo: repo}
}

// LookupByDocument busca un paciente por documento
func (s *PatientService) LookupByDocument(ctx context.Context, document string) (*domain.Patient, error) {
	return s.repo.FindByDocument(ctx, document)
}

// LookupByID busca un paciente por NumeroPaciente
func (s *PatientService) LookupByID(ctx context.Context, id string) (*domain.Patient, error) {
	return s.repo.FindByID(ctx, id)
}

// CalculateAge calcula la edad a partir de la fecha de nacimiento.
// Uses month/day comparison instead of YearDay() to handle leap years correctly.
func CalculateAge(birthDate time.Time) int {
	now := time.Now()
	age := now.Year() - birthDate.Year()
	if now.Month() < birthDate.Month() ||
		(now.Month() == birthDate.Month() && now.Day() < birthDate.Day()) {
		age--
	}
	return age
}

// FormatFullName construye el nombre completo desde partes
func FormatFullName(p *domain.Patient) string {
	if p.FullName != "" {
		return strings.TrimSpace(p.FullName)
	}
	parts := []string{}
	if p.FirstName != "" {
		parts = append(parts, p.FirstName)
	}
	if p.SecondName != "" {
		parts = append(parts, p.SecondName)
	}
	if p.FirstSurname != "" {
		parts = append(parts, p.FirstSurname)
	}
	if p.SecondSurname != "" {
		parts = append(parts, p.SecondSurname)
	}
	return strings.Join(parts, " ")
}

// FormatAge devuelve la edad como string
func FormatAge(birthDate time.Time) string {
	return fmt.Sprintf("%d", CalculateAge(birthDate))
}

// Create crea un paciente nuevo en la BD externa
func (s *PatientService) Create(ctx context.Context, input domain.CreatePatientInput) (string, error) {
	return s.repo.Create(ctx, input)
}

// UpdateEntity actualiza la entidad/EPS de un paciente
func (s *PatientService) UpdateEntity(ctx context.Context, patientID, entityCode string) error {
	return s.repo.UpdateEntity(ctx, patientID, entityCode)
}
