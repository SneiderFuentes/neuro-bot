package datosipsndx

import (
	"context"
	"database/sql"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.ProcedureRepository = (*ProcedureRepo)(nil)

type ProcedureRepo struct {
	db *sql.DB
}

func NewProcedureRepo(db *sql.DB) *ProcedureRepo {
	return &ProcedureRepo{db: db}
}

func (r *ProcedureRepo) FindByCode(ctx context.Context, code string) (*domain.Procedure, error) {
	query := `SELECT id, codigo_cups, nombre, COALESCE(descripcion, ''),
	            COALESCE(especialidad_id, 0), COALESCE(servicio_id, 0),
	            COALESCE(preparacion, ''), COALESCE(direccion, ''),
	            COALESCE(video_url, ''), COALESCE(audio_url, ''),
	            COALESCE(tipo, ''),
	            COALESCE(horario_especifico_id, 0),
	            COALESCE(activo, 1)
	          FROM cups_procedimientos
	          WHERE codigo_cups = ? AND activo = 1
	          LIMIT 1`

	var p domain.Procedure
	var active int
	err := r.db.QueryRowContext(ctx, query, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.Description,
		&p.SpecialtyID, &p.ServiceID,
		&p.Preparation, &p.Address,
		&p.VideoURL, &p.AudioURL,
		&p.Type,
		&p.SpecificScheduleID,
		&active,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsActive = (active == 1)
	return &p, nil
}

func (r *ProcedureRepo) FindByID(ctx context.Context, id int) (*domain.Procedure, error) {
	query := `SELECT id, codigo_cups, nombre, COALESCE(descripcion, ''),
	            COALESCE(especialidad_id, 0), COALESCE(servicio_id, 0),
	            COALESCE(preparacion, ''), COALESCE(direccion, ''),
	            COALESCE(video_url, ''), COALESCE(audio_url, ''),
	            COALESCE(tipo, ''),
	            COALESCE(horario_especifico_id, 0),
	            COALESCE(activo, 1)
	          FROM cups_procedimientos
	          WHERE id = ?
	          LIMIT 1`

	var p domain.Procedure
	var active int
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Code, &p.Name, &p.Description,
		&p.SpecialtyID, &p.ServiceID,
		&p.Preparation, &p.Address,
		&p.VideoURL, &p.AudioURL,
		&p.Type,
		&p.SpecificScheduleID,
		&active,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsActive = (active == 1)
	return &p, nil
}

func (r *ProcedureRepo) FindAllActive(ctx context.Context) ([]domain.Procedure, error) {
	query := `SELECT id, codigo_cups, nombre FROM cups_procedimientos WHERE activo = 1 ORDER BY codigo_cups`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []domain.Procedure
	for rows.Next() {
		var p domain.Procedure
		if err := rows.Scan(&p.ID, &p.Code, &p.Name); err != nil {
			return nil, err
		}
		p.IsActive = true
		procs = append(procs, p)
	}
	return procs, rows.Err()
}

func (r *ProcedureRepo) SearchByName(ctx context.Context, name string) ([]domain.Procedure, error) {
	query := `SELECT id, codigo_cups, nombre
	          FROM cups_procedimientos
	          WHERE nombre LIKE ? AND activo = 1
	          ORDER BY nombre
	          LIMIT 10`

	rows, err := r.db.QueryContext(ctx, query, "%"+name+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []domain.Procedure
	for rows.Next() {
		var p domain.Procedure
		if err := rows.Scan(&p.ID, &p.Code, &p.Name); err != nil {
			return nil, err
		}
		p.IsActive = true
		procs = append(procs, p)
	}
	return procs, rows.Err()
}
