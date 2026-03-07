package datosipsndx

import (
	"context"
	"database/sql"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.MunicipalityRepository = (*MunicipalityRepo)(nil)

type MunicipalityRepo struct {
	db *sql.DB
}

func NewMunicipalityRepo(db *sql.DB) *MunicipalityRepo {
	return &MunicipalityRepo{db: db}
}

func (r *MunicipalityRepo) Search(ctx context.Context, name string) ([]domain.Municipality, error) {
	query := `SELECT id, cod_departamento, departamento, cod_municipio, municipio
	          FROM municipios WHERE municipio LIKE ? ORDER BY municipio LIMIT 10`

	rows, err := r.db.QueryContext(ctx, query, "%"+name+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var municipalities []domain.Municipality
	for rows.Next() {
		var m domain.Municipality
		if err := rows.Scan(&m.ID, &m.DepartmentCode, &m.DepartmentName, &m.MunicipalityCode, &m.MunicipalityName); err != nil {
			return nil, err
		}
		municipalities = append(municipalities, m)
	}
	return municipalities, rows.Err()
}
