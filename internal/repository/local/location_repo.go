package local

import (
	"context"
	"database/sql"

	"github.com/neuro-bot/neuro-bot/internal/domain"
)

type LocationRepo struct {
	db *sql.DB
}

func NewLocationRepo(db *sql.DB) *LocationRepo {
	return &LocationRepo{db: db}
}

// FindActive returns all active center locations.
func (r *LocationRepo) FindActive(ctx context.Context) ([]domain.CenterLocation, error) {
	query := `SELECT id, name, address, phone, google_maps_url, is_active
	          FROM center_locations WHERE is_active = 1 ORDER BY name`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []domain.CenterLocation
	for rows.Next() {
		var loc domain.CenterLocation
		if err := rows.Scan(&loc.ID, &loc.Name, &loc.Address, &loc.Phone, &loc.GoogleMapsURL, &loc.IsActive); err != nil {
			return nil, err
		}
		locations = append(locations, loc)
	}
	return locations, rows.Err()
}
