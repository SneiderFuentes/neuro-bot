package domain

type Doctor struct {
	Document string
	FullName string
	CupID    int
	IsActive bool
}

type Schedule struct {
	ID             int
	DoctorDocument string
	Name           string
}

type ScheduleConfig struct {
	ID                     int
	DoctorDocument         string
	AppointmentDuration    int // minutos
	IsActive               bool
	AgendaID               int
	SessionsPerAppointment int
	WorkDays               [7]bool          // 0=domingo..6=sábado
	MorningStart           [7]string        // HH:mm por día
	MorningEnd             [7]string
	AfternoonStart         [7]string
	AfternoonEnd           [7]string
}

type WorkingDay struct {
	DoctorDocument   string
	Date             string // YYYY-MM-DD
	MorningEnabled   bool
	AfternoonEnabled bool
	AgendaID         int
}
