package domain

import "time"

// PxCita represents a procedure associated with an appointment
type PxCita struct {
	ID            int       `json:"id"`
	AppointmentID int       `json:"appointment_id"`
	CupCode       string    `json:"cup_code"`
	Quantity      int       `json:"quantity"`
	UnitValue     float64   `json:"unit_value"`
	ServiceID     int       `json:"service_id"`
	Billed        int       `json:"billed"`
	PackageID     *int      `json:"package_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreatePxCitaInput represents the input for creating a pxcita record
type CreatePxCitaInput struct {
	AppointmentID int     `json:"appointment_id"`
	CupCode       string  `json:"cup_code"`
	Quantity      int     `json:"quantity"`
	UnitValue     float64 `json:"unit_value"`
	ServiceID     int     `json:"service_id"`
}
