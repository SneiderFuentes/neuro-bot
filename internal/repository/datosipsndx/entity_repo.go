package datosipsndx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.EntityRepository = (*EntityRepo)(nil)

type EntityRepo struct {
	db *sql.DB
}

func NewEntityRepo(db *sql.DB) *EntityRepo {
	return &EntityRepo{db: db}
}

func (r *EntityRepo) FindActive(ctx context.Context) ([]domain.Entity, error) {
	query := `SELECT NoRegistro, IDEntidad, NombreEntidad, TipoPrecio, COALESCE(CategoriaEntidad, '')
	          FROM entidades WHERE contratoactivo = -1 ORDER BY NoRegistro ASC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []domain.Entity
	for rows.Next() {
		var e domain.Entity
		if err := rows.Scan(&e.ID, &e.Code, &e.Name, &e.PriceType, &e.Category); err != nil {
			return nil, err
		}
		e.IsActive = true
		entities = append(entities, e)
	}
	return entities, rows.Err()
}

func (r *EntityRepo) FindActiveByCategory(ctx context.Context, category string) ([]domain.Entity, error) {
	query := `SELECT NoRegistro, IDEntidad, NombreEntidad, TipoPrecio, COALESCE(CategoriaEntidad, '')
	          FROM entidades WHERE contratoactivo = -1 AND CategoriaEntidad = ? ORDER BY NoRegistro ASC`

	rows, err := r.db.QueryContext(ctx, query, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []domain.Entity
	for rows.Next() {
		var e domain.Entity
		if err := rows.Scan(&e.ID, &e.Code, &e.Name, &e.PriceType, &e.Category); err != nil {
			return nil, err
		}
		e.IsActive = true
		entities = append(entities, e)
	}
	return entities, rows.Err()
}

func (r *EntityRepo) GetCodeByIndexAndCategory(ctx context.Context, index int, category string) (string, error) {
	entities, err := r.FindActiveByCategory(ctx, category)
	if err != nil {
		return "", err
	}
	arrayIndex := index - 1
	if arrayIndex < 0 || arrayIndex >= len(entities) {
		return "", fmt.Errorf("entity index %d out of range (max %d)", index, len(entities))
	}
	return entities[arrayIndex].Code, nil
}

func (r *EntityRepo) FindByCode(ctx context.Context, code string) (*domain.Entity, error) {
	query := `SELECT NoRegistro, IDEntidad, NombreEntidad, COALESCE(TipoPrecio, ''), contratoactivo
	          FROM entidades WHERE IDEntidad = ?`

	var e domain.Entity
	var active int
	err := r.db.QueryRowContext(ctx, query, code).Scan(&e.ID, &e.Code, &e.Name, &e.PriceType, &active)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.IsActive = (active == -1)
	return &e, nil
}
