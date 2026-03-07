package repository

import (
	"context"

	"github.com/neuro-bot/neuro-bot/internal/domain"
)

// PatientRepository — operaciones sobre pacientes en BD externa.
// Implementación actual: datosipsndx (tabla: pacientes)
// Al migrar de software: crear nueva implementación con misma interfaz.
type PatientRepository interface {
	FindByDocument(ctx context.Context, doc string) (*domain.Patient, error)
	FindByID(ctx context.Context, id string) (*domain.Patient, error)
	Create(ctx context.Context, input domain.CreatePatientInput) (string, error)
	UpdateEntity(ctx context.Context, patientID, entityCode string) error
}

// AppointmentRepository — operaciones sobre citas en BD externa.
// Implementación actual: datosipsndx (tablas: citas, pxcita)
type AppointmentRepository interface {
	FindByID(ctx context.Context, id string) (*domain.Appointment, error)
	FindUpcomingByPatient(ctx context.Context, patientID string) ([]domain.Appointment, error)
	FindByAgendaAndDate(ctx context.Context, agendaID int, date string) ([]domain.Appointment, error)
	Create(ctx context.Context, input domain.CreateAppointmentInput) (*domain.Appointment, error)
	Confirm(ctx context.Context, id string, channel, channelID string) error
	Cancel(ctx context.Context, id string, reason, channel, channelID string) error
	ConfirmBatch(ctx context.Context, ids []string, channel, channelID string) error
	CancelBatch(ctx context.Context, ids []string, reason, channel, channelID string) error
	HasFutureForCup(ctx context.Context, patientID, cupCode string) (bool, error)
	FindLastDoctorForCups(ctx context.Context, patientID string, cups []string) (string, error)
	CountMonthlyByGroup(ctx context.Context, cupsCodes []string) (int, error)
	FindPendingByDate(ctx context.Context, date string) ([]domain.Appointment, error)
	RescheduleDate(ctx context.Context, agendaID int, doctorDoc, oldDate, newDate string) (int, error)
}

// DoctorRepository — búsqueda de médicos por procedimiento.
// Implementación actual: datosipsndx (tabla: cup_medico)
type DoctorRepository interface {
	FindByCupID(ctx context.Context, cupID int) ([]domain.Doctor, error)
	FindByCupsCode(ctx context.Context, cupsCode string) ([]domain.Doctor, error)
	FindByDocument(ctx context.Context, doc string) (*domain.Doctor, error)
}

// ScheduleRepository — agendas, horarios y excepciones.
// Implementación actual: datosipsndx (tablas: tblagendas, citas_conf, tblexepciondias)
type ScheduleRepository interface {
	FindFutureWorkingDays(ctx context.Context, doctorDocs []string) ([]domain.WorkingDay, error)
	FindScheduleConfig(ctx context.Context, scheduleID int, doctorDoc string) (*domain.ScheduleConfig, error)
	FindByScheduleID(ctx context.Context, scheduleID int, scheduleType string) (*domain.Schedule, error)
	FindBookedSlots(ctx context.Context, agendaID int, date string) ([]string, error)
	FindWorkingDayException(ctx context.Context, agendaID int, doctorDoc, date string) (*domain.WorkingDay, error)
	UpdateWorkingDayExceptionDate(ctx context.Context, agendaID int, doctorDoc, oldDate, newDate string) (bool, error)
	DeleteWorkingDayException(ctx context.Context, agendaID int, doctorDoc, date string) (bool, error)
}

// ProcedureRepository — catálogo de procedimientos CUPS.
// Implementación actual: datosipsndx (tabla: cups_procedimientos)
type ProcedureRepository interface {
	FindByCode(ctx context.Context, code string) (*domain.Procedure, error)
	FindByID(ctx context.Context, id int) (*domain.Procedure, error)
	SearchByName(ctx context.Context, name string) ([]domain.Procedure, error)
	FindAllActive(ctx context.Context) ([]domain.Procedure, error)
}

// EntityRepository — entidades/EPS activas.
// Implementación actual: datosipsndx (tabla: entidades)
type EntityRepository interface {
	FindActive(ctx context.Context) ([]domain.Entity, error)
	FindActiveByCategory(ctx context.Context, category string) ([]domain.Entity, error)
	FindByCode(ctx context.Context, code string) (*domain.Entity, error)
	GetCodeByIndexAndCategory(ctx context.Context, index int, category string) (string, error)
}

// MunicipalityRepository — búsqueda de municipios.
// Implementación actual: datosipsndx (tabla: municipios)
type MunicipalityRepository interface {
	Search(ctx context.Context, name string) ([]domain.Municipality, error)
}

// SoatRepository — tarifas SOAT por procedimiento.
// Implementación actual: datosipsndx (tabla: codigossoat)
type SoatRepository interface {
	FindPrice(ctx context.Context, cupCode, entityCode string) (float64, error)
}

// Repositories agrupa todas las interfaces para inyección de dependencias.
type Repositories struct {
	Patient      PatientRepository
	Appointment  AppointmentRepository
	Doctor       DoctorRepository
	Schedule     ScheduleRepository
	Procedure    ProcedureRepository
	Entity       EntityRepository
	Municipality MunicipalityRepository
	Soat         SoatRepository
}
