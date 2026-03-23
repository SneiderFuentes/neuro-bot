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
	query := `SELECT p.id, p.codigo_cups, p.nombre, COALESCE(p.descripcion, ''),
	            COALESCE(p.especialidad_id, 0), COALESCE(CAST(p.servicio_id AS SIGNED), 0),
	            COALESCE(p.servicio, ''),
	            COALESCE(p.preparacion, ''), COALESCE(p.direccion, ''),
	            COALESCE(p.video_url, ''), COALESCE(p.audio_url, ''),
	            COALESCE(p.tipo, ''),
	            COALESCE(p.horario_especifico_id, 0),
	            COALESCE(p.activo, 1)
	          FROM cups_procedimientos p
	          WHERE p.codigo_cups = ? AND p.activo = 1
	          LIMIT 1`

	var p domain.Procedure
	var active int
	err := r.db.QueryRowContext(ctx, query, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.Description,
		&p.SpecialtyID, &p.ServiceID,
		&p.ServiceName,
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
	query := `SELECT p.id, p.codigo_cups, p.nombre, COALESCE(p.descripcion, ''),
	            COALESCE(p.especialidad_id, 0), COALESCE(CAST(p.servicio_id AS SIGNED), 0),
	            COALESCE(p.servicio, ''),
	            COALESCE(p.preparacion, ''), COALESCE(p.direccion, ''),
	            COALESCE(p.video_url, ''), COALESCE(p.audio_url, ''),
	            COALESCE(p.tipo, ''),
	            COALESCE(p.horario_especifico_id, 0),
	            COALESCE(p.activo, 1)
	          FROM cups_procedimientos p
	          WHERE p.id = ?
	          LIMIT 1`

	var p domain.Procedure
	var active int
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Code, &p.Name, &p.Description,
		&p.SpecialtyID, &p.ServiceID,
		&p.ServiceName,
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
