package services

import (
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/domain"
)

// AddressMapper maps procedure addresses to Google Maps URLs using center_locations data.
type AddressMapper struct {
	locations  []domain.CenterLocation
	defaultURL string
}

// NewAddressMapper creates a mapper from pre-loaded locations.
// The last location's Maps URL is used as default fallback.
func NewAddressMapper(locations []domain.CenterLocation) *AddressMapper {
	m := &AddressMapper{locations: locations}
	if len(locations) > 0 {
		m.defaultURL = locations[len(locations)-1].GoogleMapsURL
	}
	return m
}

// MapsURL returns the Google Maps URL for a procedure address by matching
// against known center_locations addresses. Uses partial matching (street number).
// Returns empty string if no locations are configured.
func (m *AddressMapper) MapsURL(address string) string {
	if len(m.locations) == 0 || address == "" {
		return m.defaultURL
	}

	addr := strings.ToLower(address)
	for _, loc := range m.locations {
		locAddr := strings.ToLower(loc.Address)
		// Match by shared street identifier (e.g. "calle 35", "calle 34")
		for _, word := range strings.Fields(locAddr) {
			if len(word) >= 2 && isDigit(word[0]) && strings.Contains(addr, word) {
				// Found a number match — verify the street name also matches
				if shareStreetName(addr, locAddr) {
					return loc.GoogleMapsURL
				}
			}
		}
	}
	return m.defaultURL
}

// FormatAddress returns the formatted address with Google Maps link.
// Example: "*Dirección:* Calle 34 No 38-47 Barzal\n📍 Ver en Google Maps: https://..."
func (m *AddressMapper) FormatAddress(address string) string {
	if address == "" {
		if len(m.locations) > 0 {
			last := m.locations[len(m.locations)-1]
			address = last.Address
		}
	}
	mapsURL := m.MapsURL(address)
	result := "*Dirección:* " + address
	if mapsURL != "" {
		result += "\n📍 Ver en Google Maps: " + mapsURL
	}
	return result
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// shareStreetName checks if both addresses share a street keyword like "calle" or "carrera".
func shareStreetName(a, b string) bool {
	streets := []string{"calle", "carrera", "avenida", "transversal", "diagonal"}
	for _, s := range streets {
		if strings.Contains(a, s) && strings.Contains(b, s) {
			return true
		}
	}
	return false
}
