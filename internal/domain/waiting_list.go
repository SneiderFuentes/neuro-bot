package domain

import "time"

// WaitingListEntry represents a patient waiting for slot availability.
type WaitingListEntry struct {
	ID              string
	PhoneNumber     string
	PatientID       string
	PatientDoc      string
	PatientName     string
	PatientAge      int
	PatientGender   string
	PatientEntity   string
	CupsCode        string
	CupsName        string
	IsContrasted    bool
	IsSedated       bool
	Espacios        int
	ProceduresJSON  string
	GfrCreatinine   float64
	GfrHeightCm     int
	GfrWeightKg     float64
	GfrDiseaseType  string
	GfrCalculated   float64
	IsPregnant      bool
	BabyWeightCat   string
	PreferredDoctorDoc string
	Status          string // waiting, notified, scheduled, declined, expired, duplicate_found
	NotifiedAt      *time.Time
	ResolvedAt      *time.Time
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// WaitingListFilters for querying the waiting list.
type WaitingListFilters struct {
	Status   string
	CupsCode string
}
