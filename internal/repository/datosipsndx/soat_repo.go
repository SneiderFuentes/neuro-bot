package datosipsndx

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.SoatRepository = (*SoatRepo)(nil)

type SoatRepo struct {
	db *sql.DB
}

func NewSoatRepo(db *sql.DB) *SoatRepo {
	return &SoatRepo{db: db}
}

// FindPrice retrieves the price for a CUPS code based on the tariff type (e.g., "tariff_01")
func (r *SoatRepo) FindPrice(ctx context.Context, cupCode, tariffType string) (float64, error) {
	if tariffType == "" {
		return 0, nil
	}
	// Normalize tariff type: "01" -> "Tarifa01", "1" -> "Tarifa01"
	var columnName string
	if len(tariffType) == 1 {
		columnName = fmt.Sprintf("Tarifa0%s", tariffType)
	} else if len(tariffType) == 2 {
		columnName = fmt.Sprintf("Tarifa%s", tariffType)
	} else if len(tariffType) > 7 && tariffType[:7] == "tariff_" {
		// Format: "tariff_01" -> "Tarifa01"
		columnName = fmt.Sprintf("Tarifa%s", tariffType[7:])
	} else {
		columnName = tariffType // Use as-is if already formatted
	}

	query := fmt.Sprintf("SELECT COALESCE(%s, 0) FROM codigossoat WHERE CodigoCUPS = ?", columnName)
	
	var price float64
	err := r.db.QueryRowContext(ctx, query, cupCode).Scan(&price)
	if err == sql.ErrNoRows {
		return 0, nil // No price found, return 0
	}
	if err != nil {
		return 0, fmt.Errorf("find soat price: %w", err)
	}
	
	return price, nil
}
