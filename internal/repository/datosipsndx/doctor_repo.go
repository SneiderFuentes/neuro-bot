package datosipsndx

import (
	"context"
	"database/sql"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.DoctorRepository = (*DoctorRepo)(nil)

type DoctorRepo struct {
	db *sql.DB
}

func NewDoctorRepo(db *sql.DB) *DoctorRepo {
	return &DoctorRepo{db: db}
}

func (r *DoctorRepo) FindByCupID(ctx context.Context, cupID int) ([]domain.Doctor, error) {
	query := `SELECT doctor_documento, doctor_nombre_completo, cup_id, activo
	          FROM cup_medico
	          WHERE cup_id = ? AND activo = 1`

	rows, err := r.db.QueryContext(ctx, query, cupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var doctors []domain.Doctor
	for rows.Next() {
		var d domain.Doctor
		var active int
		if err := rows.Scan(&d.Document, &d.FullName, &d.CupID, &active); err != nil {
			return nil, err
		}
		d.IsActive = (active == 1)
		doctors = append(doctors, d)
	}
	return doctors, rows.Err()
}

func (r *DoctorRepo) FindByCupsCode(ctx context.Context, cupsCode string) ([]domain.Doctor, error) {
	query := `SELECT cm.doctor_documento, cm.doctor_nombre_completo, cm.cup_id, cm.activo
	          FROM cup_medico cm
	          INNER JOIN cups_procedimientos cp ON cp.id = cm.cup_id
	          WHERE cp.codigo_cups = ? AND cm.activo = 1`

	rows, err := r.db.QueryContext(ctx, query, cupsCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var doctors []domain.Doctor
	for rows.Next() {
		var d domain.Doctor
		var active int
		if err := rows.Scan(&d.Document, &d.FullName, &d.CupID, &active); err != nil {
			return nil, err
		}
		d.IsActive = (active == 1)
		doctors = append(doctors, d)
	}
	return doctors, rows.Err()
}

func (r *DoctorRepo) FindByDocument(ctx context.Context, doc string) (*domain.Doctor, error) {
	query := `SELECT doctor_documento, doctor_nombre_completo, cup_id, activo
	          FROM cup_medico
	          WHERE doctor_documento = ? AND activo = 1
	          LIMIT 1`

	var d domain.Doctor
	var active int
	err := r.db.QueryRowContext(ctx, query, doc).Scan(&d.Document, &d.FullName, &d.CupID, &active)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.IsActive = (active == 1)
	return &d, nil
}
