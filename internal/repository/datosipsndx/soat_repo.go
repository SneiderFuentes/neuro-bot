package datosipsndx

import (
	"context"
	"database/sql"

	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.SoatRepository = (*SoatRepo)(nil)

type SoatRepo struct {
	db *sql.DB
}

func NewSoatRepo(db *sql.DB) *SoatRepo {
	return &SoatRepo{db: db}
}

func (r *SoatRepo) FindPrice(ctx context.Context, cupCode, entityCode string) (float64, error) {
	// Se implementará en Fase 9 (Validaciones médicas)
	return 0, nil
}
