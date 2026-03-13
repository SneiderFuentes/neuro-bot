package datosipsndx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.PatientRepository = (*PatientRepo)(nil)

type PatientRepo struct {
	db *sql.DB
}

func NewPatientRepo(db *sql.DB) *PatientRepo {
	return &PatientRepo{db: db}
}

func (r *PatientRepo) FindByDocument(ctx context.Context, doc string) (*domain.Patient, error) {
	query := `SELECT NumeroPaciente, COALESCE(TipoID,''), IDPaciente,
	          COALESCE(Nombre1,''), COALESCE(Nombre2,''), COALESCE(Apellido1,''), COALESCE(Apellido2,''), COALESCE(NCompleto,''),
	          FechaNacimiento, COALESCE(SexoPaciente,''), COALESCE(Telefono,''), COALESCE(CorreoE,''), COALESCE(EntidadPaciente,''),
	          COALESCE(Direccion,''), COALESCE(Municipio,'')
	          FROM pacientes WHERE IDPaciente = ?`

	return r.scanPatient(r.db.QueryRowContext(ctx, query, doc))
}

func (r *PatientRepo) FindByID(ctx context.Context, id string) (*domain.Patient, error) {
	query := `SELECT NumeroPaciente, COALESCE(TipoID,''), IDPaciente,
	          COALESCE(Nombre1,''), COALESCE(Nombre2,''), COALESCE(Apellido1,''), COALESCE(Apellido2,''), COALESCE(NCompleto,''),
	          FechaNacimiento, COALESCE(SexoPaciente,''), COALESCE(Telefono,''), COALESCE(CorreoE,''), COALESCE(EntidadPaciente,''),
	          COALESCE(Direccion,''), COALESCE(Municipio,'')
	          FROM pacientes WHERE NumeroPaciente = ?`

	return r.scanPatient(r.db.QueryRowContext(ctx, query, id))
}

// scanPatient scans a single patient row. Uses COALESCE in queries to handle NULLs.
func (r *PatientRepo) scanPatient(row *sql.Row) (*domain.Patient, error) {
	p := &domain.Patient{}
	err := row.Scan(
		&p.ID, &p.DocumentType, &p.DocumentNumber,
		&p.FirstName, &p.SecondName, &p.FirstSurname, &p.SecondSurname, &p.FullName,
		&p.BirthDate, &p.Gender, &p.Phone, &p.Email, &p.EntityCode,
		&p.Address, &p.CityCode,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *PatientRepo) Create(ctx context.Context, input domain.CreatePatientInput) (string, error) {
	nameParts := []string{}
	for _, part := range []string{input.FirstName, input.SecondName, input.FirstSurname, input.SecondSurname} {
		if strings.TrimSpace(part) != "" {
			nameParts = append(nameParts, strings.TrimSpace(part))
		}
	}
	fullName := strings.Join(nameParts, " ")
	now := time.Now()

	countryCode := input.CountryCode
	if countryCode == "" {
		countryCode = "170" // Colombia
	}

	// Integer columns require numeric values — default to "0" when empty.
	level := input.Level
	if level == "" {
		level = "0"
	}
	educationLevel := input.EducationLevel
	if educationLevel == "" {
		educationLevel = "0"
	}

	// Truncate strings to match DB column char limits (STRICT_TRANS_TABLES rejects overflow).
	firstName := truncate(input.FirstName, 20)
	secondName := truncate(input.SecondName, 20)
	address := truncate(input.Address, 59)
	occupation := truncate(input.Occupation, 30)
	fullName = truncate(fullName, 80)

	query := `INSERT INTO pacientes (
		TipoID, IDPaciente, LugarExpedicion,
		Apellido1, Apellido2, Nombre1, Nombre2, NCompleto,
		TipoAfiliacion, TipoUsuario,
		FechaNacimiento, SexoPaciente,
		Direccion, Municipio, Zona,
		Telefono, CorreoE, Ocupacion,
		EntidadPaciente, Nivel, EstadoCivil,
		LugarNacimiento, Escolaridad, codPaisOrigen,
		FechaCreado, CreadoPor
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := r.db.ExecContext(ctx, query,
		input.DocumentType, input.DocumentNumber, input.DocumentIssuePlace,
		input.FirstSurname, input.SecondSurname, firstName, secondName, fullName,
		input.AffiliationType, input.UserType,
		input.BirthDate, input.Gender,
		address, input.CityCode, input.Zone,
		input.Phone, input.Email, occupation,
		input.EntityCode, level, input.MaritalStatus,
		input.BirthPlace, educationLevel, countryCode,
		now, 0,
	)
	if err != nil {
		return "", fmt.Errorf("insert pacientes: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return "", fmt.Errorf("last insert id: %w", err)
	}
	return fmt.Sprintf("%d", id), nil
}

func (r *PatientRepo) UpdateEntity(ctx context.Context, patientID, entityCode string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE pacientes SET EntidadPaciente = ? WHERE NumeroPaciente = ?`,
		entityCode, patientID)
	if err != nil {
		return fmt.Errorf("update entity: %w", err)
	}
	return nil
}

func (r *PatientRepo) UpdateContactInfo(ctx context.Context, patientID, phone, email string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE pacientes SET Telefono = ?, CorreoE = ?, FechaModificado = NOW(), ModificadoPor = 0 WHERE NumeroPaciente = ?`,
		phone, email, patientID)
	if err != nil {
		return fmt.Errorf("update contact info: %w", err)
	}
	return nil
}

// truncate returns s trimmed to maxLen characters (rune-safe).
func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) > maxLen {
		return string(r[:maxLen])
	}
	return s
}
