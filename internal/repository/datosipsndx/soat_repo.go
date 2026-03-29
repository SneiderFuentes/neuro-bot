package datosipsndx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var validTarifaCol = regexp.MustCompile(`^Tarifa\d{2}$`)

var _ repository.SoatRepository = (*SoatRepo)(nil)

type SoatRepo struct {
	db *sql.DB
}

func NewSoatRepo(db *sql.DB) *SoatRepo {
	return &SoatRepo{db: db}
}

// FindPrice retrieves the price for a CUPS code based on the tariff type (e.g., "tariff_01").
// Returns nil when the CUPS code is not found or tariffType is empty.
// Returns *0.0 when the tariff is legitimately zero in the database.
func (r *SoatRepo) FindPrice(ctx context.Context, cupCode, tariffType string) (*float64, error) {
	if tariffType == "" {
		return nil, nil
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

	if !validTarifaCol.MatchString(columnName) {
		return nil, fmt.Errorf("invalid tariff column: %s", columnName)
	}

	query := fmt.Sprintf("SELECT COALESCE(%s, 0) FROM codigossoat WHERE CodigoCUPS = ?", columnName)

	var price float64
	err := r.db.QueryRowContext(ctx, query, cupCode).Scan(&price)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find soat price: %w", err)
	}

	return &price, nil
}
