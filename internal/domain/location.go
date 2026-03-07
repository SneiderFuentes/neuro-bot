package domain

// CenterLocation represents a physical location/branch of the center.
type CenterLocation struct {
	ID            int
	Name          string
	Address       string
	Phone         string
	GoogleMapsURL string
	IsActive      bool
}
