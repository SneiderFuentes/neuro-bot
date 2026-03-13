package datosipsndx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
)

var _ repository.AppointmentRepository = (*AppointmentRepo)(nil)

type AppointmentRepo struct {
	db *sql.DB
}

func NewAppointmentRepo(db *sql.DB) *AppointmentRepo {
	return &AppointmentRepo{db: db}
}

func (r *AppointmentRepo) FindByID(ctx context.Context, id string) (*domain.Appointment, error) {
	query := `SELECT c.IdCita, c.FechaSolicitud, c.FeCita, c.FechaCita, c.IdMedico,
	            COALESCE(cm.doctor_nombre_completo, c.IdMedico) AS DoctorName,
	            c.NumeroPaciente, c.Entidad, c.Agenda,
	            c.Cancelada, c.FechaCancelacion,
	            c.Confirmada, c.FechaConfirmacion, c.MedioConfirmacion, c.IdMedioConfirmacion,
	            c.Cumplida, c.Observaciones, c.Remonte
	          FROM citas c
	          LEFT JOIN cup_medico cm ON cm.doctor_documento = c.IdMedico AND cm.activo = 1
	            AND cm.id = (SELECT MIN(cm2.id) FROM cup_medico cm2 WHERE cm2.doctor_documento = c.IdMedico AND cm2.activo = 1)
	          WHERE c.IdCita = ? AND c.Remonte = 0`

	var appt domain.Appointment
	var requestDate sql.NullTime
	var cancelDate sql.NullTime
	var confirmDate sql.NullTime
	var canceledInt, confirmedInt, fulfilledInt int
	var confirmChannel, confirmChannelID, observations sql.NullString

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&appt.ID, &requestDate, &appt.Date, &appt.TimeSlot, &appt.DoctorID,
		&appt.DoctorName,
		&appt.PatientID, &appt.Entity, &appt.AgendaID,
		&canceledInt, &cancelDate,
		&confirmedInt, &confirmDate, &confirmChannel, &confirmChannelID,
		&fulfilledInt, &observations, &appt.Remonte,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if requestDate.Valid {
		appt.RequestDate = requestDate.Time
	}
	appt.Canceled = (canceledInt == -1)
	if cancelDate.Valid {
		appt.CancelDate = &cancelDate.Time
	}
	appt.Confirmed = (confirmedInt == -1)
	if confirmDate.Valid {
		appt.ConfirmationDate = &confirmDate.Time
	}
	if confirmChannel.Valid {
		appt.ConfirmationChannel = confirmChannel.String
	}
	if confirmChannelID.Valid {
		appt.ConfirmationChannelID = confirmChannelID.String
	}
	appt.Fulfilled = (fulfilledInt == -1)
	if observations.Valid {
		appt.Observations = observations.String
	}

	procs, err := r.fetchProcedures(ctx, appt.ID)
	if err != nil {
		return nil, err
	}
	appt.Procedures = procs

	return &appt, nil
}

func (r *AppointmentRepo) FindUpcomingByPatient(ctx context.Context, patientID string) ([]domain.Appointment, error) {
	query := `SELECT c.IdCita, c.FeCita, c.FechaCita, c.IdMedico,
	            COALESCE(cm.doctor_nombre_completo, c.IdMedico) AS DoctorName,
	            c.NumeroPaciente, c.Entidad, c.Agenda, c.Confirmada, c.Observaciones
	          FROM citas c
	          LEFT JOIN cup_medico cm ON cm.doctor_documento = c.IdMedico AND cm.activo = 1
	            AND cm.id = (SELECT MIN(cm2.id) FROM cup_medico cm2 WHERE cm2.doctor_documento = c.IdMedico AND cm2.activo = 1)
	          WHERE c.NumeroPaciente = ? AND c.FeCita >= CURDATE()
	            AND c.Cancelada = 0 AND c.Remonte = 0
	          ORDER BY c.FeCita, c.FechaCita`

	rows, err := r.db.QueryContext(ctx, query, patientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var appointments []domain.Appointment
	var ids []string
	for rows.Next() {
		var appt domain.Appointment
		var confirmedInt int
		var observations sql.NullString

		if err := rows.Scan(
			&appt.ID, &appt.Date, &appt.TimeSlot, &appt.DoctorID,
			&appt.DoctorName, &appt.PatientID, &appt.Entity, &appt.AgendaID,
			&confirmedInt, &observations,
		); err != nil {
			return nil, err
		}

		appt.Confirmed = (confirmedInt == -1)
		if observations.Valid {
			appt.Observations = observations.String
		}

		appointments = append(appointments, appt)
		ids = append(ids, appt.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		procMap, err := r.fetchProceduresBatch(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range appointments {
			appointments[i].Procedures = procMap[appointments[i].ID]
		}
	}

	return appointments, nil
}

func (r *AppointmentRepo) fetchProcedures(ctx context.Context, appointmentID string) ([]domain.AppointmentProcedure, error) {
	query := `SELECT px.RegistroNo, px.CUPS,
	            COALESCE(cp.nombre, px.CUPS) AS CupName,
	            px.Cantidad, px.VrUnitario, px.IdServicio
	          FROM pxcita px
	          LEFT JOIN cups_procedimientos cp ON cp.codigo_cups = px.CUPS
	          WHERE px.IdCita = ?`

	rows, err := r.db.QueryContext(ctx, query, appointmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var procs []domain.AppointmentProcedure
	for rows.Next() {
		var p domain.AppointmentProcedure
		if err := rows.Scan(&p.ID, &p.CupCode, &p.CupName, &p.Quantity, &p.UnitValue, &p.ServiceID); err != nil {
			return nil, err
		}
		p.AppointmentID = appointmentID
		procs = append(procs, p)
	}
	return procs, rows.Err()
}

func (r *AppointmentRepo) fetchProceduresBatch(ctx context.Context, appointmentIDs []string) (map[string][]domain.AppointmentProcedure, error) {
	if len(appointmentIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(appointmentIDs))
	args := make([]interface{}, len(appointmentIDs))
	for i, id := range appointmentIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT px.RegistroNo, px.IdCita, px.CUPS,
	            COALESCE(cp.nombre, px.CUPS) AS CupName,
	            px.Cantidad, px.VrUnitario, px.IdServicio
	          FROM pxcita px
	          LEFT JOIN cups_procedimientos cp ON cp.codigo_cups = px.CUPS
	          WHERE px.IdCita IN (%s)`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]domain.AppointmentProcedure)
	for rows.Next() {
		var p domain.AppointmentProcedure
		if err := rows.Scan(&p.ID, &p.AppointmentID, &p.CupCode, &p.CupName, &p.Quantity, &p.UnitValue, &p.ServiceID); err != nil {
			return nil, err
		}
		result[p.AppointmentID] = append(result[p.AppointmentID], p)
	}
	return result, rows.Err()
}

// mapToMedioConfirmacion maps channel source strings to valid ENUM('whatsapp','voz') values.
// Returns "" for unknown channels (stored as NULL via NULLIF).
func mapToMedioConfirmacion(channel string) string {
	if strings.Contains(channel, "whatsapp") {
		return "whatsapp"
	}
	if channel == "voz" {
		return "voz"
	}
	return ""
}

func (r *AppointmentRepo) Confirm(ctx context.Context, id string, channel, channelID string) error {
	medio := mapToMedioConfirmacion(channel)
	_, err := r.db.ExecContext(ctx,
		`UPDATE citas SET Confirmada = -1, Cancelada = 0,
		        FechaConfirmacion = NOW(), FechaCancelacion = NULL,
		        MedioConfirmacion = NULLIF(?, ''), IdMedioConfirmacion = NULLIF(?, '')
		 WHERE IdCita = ?`,
		medio, channelID, id)
	return err
}

func (r *AppointmentRepo) Cancel(ctx context.Context, id string, reason, channel, channelID string) error {
	medio := mapToMedioConfirmacion(channel)
	observation := fmt.Sprintf(" [Cancelada via %s: %s]", channel, reason)
	_, err := r.db.ExecContext(ctx,
		`UPDATE citas SET Cancelada = -1, Confirmada = 0,
		        FechaCancelacion = NOW(), FechaConfirmacion = NULL,
		        MedioConfirmacion = NULLIF(?, ''), IdMedioConfirmacion = NULLIF(?, ''),
		        Observaciones = CONCAT(COALESCE(Observaciones, ''), CONVERT(? USING latin1))
		 WHERE IdCita = ?`,
		medio, channelID, observation, id)
	return err
}

func (r *AppointmentRepo) ConfirmBatch(ctx context.Context, ids []string, channel, channelID string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx confirm batch: %w", err)
	}
	defer tx.Rollback()

	medio := mapToMedioConfirmacion(channel)
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE citas SET Confirmada = -1, Cancelada = 0,
			        FechaConfirmacion = NOW(), FechaCancelacion = NULL,
			        MedioConfirmacion = NULLIF(?, ''), IdMedioConfirmacion = NULLIF(?, '')
			 WHERE IdCita = ?`,
			medio, channelID, id); err != nil {
			return fmt.Errorf("confirm %s: %w", id, err)
		}
	}
	return tx.Commit()
}

func (r *AppointmentRepo) CancelBatch(ctx context.Context, ids []string, reason, channel, channelID string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx cancel batch: %w", err)
	}
	defer tx.Rollback()

	medio := mapToMedioConfirmacion(channel)
	observation := fmt.Sprintf(" [Cancelada via %s: %s]", channel, reason)
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE citas SET Cancelada = -1, Confirmada = 0,
			        FechaCancelacion = NOW(), FechaConfirmacion = NULL,
			        MedioConfirmacion = NULLIF(?, ''), IdMedioConfirmacion = NULLIF(?, ''),
			        Observaciones = CONCAT(COALESCE(Observaciones, ''), CONVERT(? USING latin1))
			 WHERE IdCita = ?`,
			medio, channelID, observation, id); err != nil {
			return fmt.Errorf("cancel %s: %w", id, err)
		}
	}
	return tx.Commit()
}

func (r *AppointmentRepo) FindByAgendaAndDate(ctx context.Context, agendaID int, date string) ([]domain.Appointment, error) {
	query := `SELECT c.IdCita, c.FeCita, c.FechaCita, c.IdMedico,
	            COALESCE(cm.doctor_nombre_completo, c.IdMedico) AS DoctorName,
	            c.NumeroPaciente,
	            COALESCE(p.NCompleto, '') AS PatientName,
	            COALESCE(p.Telefono, '') AS PatientPhone,
	            c.Entidad, c.Agenda, c.Cancelada, c.Observaciones
	          FROM citas c
	          LEFT JOIN cup_medico cm ON cm.doctor_documento = c.IdMedico AND cm.activo = 1
	            AND cm.id = (SELECT MIN(cm2.id) FROM cup_medico cm2 WHERE cm2.doctor_documento = c.IdMedico AND cm2.activo = 1)
	          LEFT JOIN pacientes p ON p.NumeroPaciente = c.NumeroPaciente
	          WHERE c.Agenda = ? AND c.FeCita = ?
	            AND c.Cancelada = 0 AND c.Remonte = 0
	          ORDER BY c.FechaCita`

	rows, err := r.db.QueryContext(ctx, query, agendaID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var appointments []domain.Appointment
	for rows.Next() {
		var appt domain.Appointment
		var canceledInt int
		var observations sql.NullString

		if err := rows.Scan(
			&appt.ID, &appt.Date, &appt.TimeSlot, &appt.DoctorID,
			&appt.DoctorName, &appt.PatientID, &appt.PatientName, &appt.PatientPhone,
			&appt.Entity, &appt.AgendaID,
			&canceledInt, &observations,
		); err != nil {
			return nil, err
		}

		appt.Canceled = (canceledInt == -1)
		if observations.Valid {
			appt.Observations = observations.String
		}

		appointments = append(appointments, appt)
	}
	return appointments, rows.Err()
}

func (r *AppointmentRepo) Create(ctx context.Context, input domain.CreateAppointmentInput) (*domain.Appointment, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify slot not taken (lock row to prevent race condition)
	var existingCount int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM citas
		 WHERE Agenda = ? AND FechaCita = ? AND Cancelada = 0 AND Remonte = 0
		 FOR UPDATE`,
		input.AgendaID, input.TimeSlot).Scan(&existingCount)
	if err != nil {
		return nil, fmt.Errorf("check slot: %w", err)
	}
	if existingCount > 0 {
		return nil, fmt.Errorf("slot_taken")
	}

	// Build observations
	var obsValue interface{}
	if input.Observations != "" {
		obsValue = input.Observations
	}

	// Insert cita
	result, err := tx.ExecContext(ctx,
		`INSERT INTO citas (FechaSolicitud, FeCita, FechaCita, IdMedico, NumeroPaciente,
		  Entidad, Agenda, FechaPideUsuario, Cancelada, Confirmada, CreadoPor, Observaciones)
		 VALUES (NOW(), ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		input.Date, input.TimeSlot, input.DoctorID, input.PatientID,
		input.Entity, input.AgendaID, input.Date, input.CreatedBy, obsValue)
	if err != nil {
		return nil, fmt.Errorf("insert cita: %w", err)
	}

	lastID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	apptID := fmt.Sprintf("%d", lastID)

	// Insert procedures into pxcita
	for _, proc := range input.Procedures {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO pxcita (FechaCreado, IdCita, CUPS, Cantidad, VrUnitario, IdServicio, Facturado, IdPaquete)
			 VALUES (NOW(), ?, ?, ?, ?, ?, 0, 0)`,
			apptID, proc.CupCode, proc.Quantity, proc.UnitValue, proc.ServiceID)
		if err != nil {
			return nil, fmt.Errorf("insert pxcita: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	appt := &domain.Appointment{
		ID:           apptID,
		Date:         input.Date,
		TimeSlot:     input.TimeSlot,
		DoctorID:     input.DoctorID,
		PatientID:    input.PatientID,
		Entity:       input.Entity,
		AgendaID:     input.AgendaID,
		Observations: input.Observations,
	}

	return appt, nil
}

func (r *AppointmentRepo) HasFutureForCup(ctx context.Context, patientID, cupCode string) (bool, error) {
	query := `SELECT 1 FROM citas c
	          INNER JOIN pxcita px ON px.IdCita = c.IdCita
	          WHERE c.NumeroPaciente = ? AND px.CUPS = ?
	            AND c.FeCita >= CURDATE() AND c.Cancelada = 0 AND c.Remonte = 0
	          LIMIT 1`

	var exists int
	err := r.db.QueryRowContext(ctx, query, patientID, cupCode).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *AppointmentRepo) FindLastDoctorForCups(ctx context.Context, patientID string, cups []string) (string, error) {
	if len(cups) == 0 {
		return "", nil
	}

	placeholders := make([]string, len(cups))
	args := make([]interface{}, 0, len(cups)+1)
	args = append(args, patientID)
	for i, c := range cups {
		placeholders[i] = "?"
		args = append(args, c)
	}

	query := fmt.Sprintf(`SELECT c.IdMedico FROM citas c
	          INNER JOIN pxcita px ON px.IdCita = c.IdCita
	          WHERE c.NumeroPaciente = ? AND px.CUPS IN (%s)
	            AND c.Cancelada = 0 AND c.Remonte = 0
	          ORDER BY c.FeCita DESC
	          LIMIT 1`, strings.Join(placeholders, ","))

	var doctorDoc string
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&doctorDoc)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return doctorDoc, nil
}

func (r *AppointmentRepo) CountMonthlyByGroup(ctx context.Context, cupsCodes []string, year, month int) (int, error) {
	if len(cupsCodes) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(cupsCodes))
	args := make([]interface{}, len(cupsCodes))
	for i, c := range cupsCodes {
		placeholders[i] = "?"
		args[i] = c
	}

	query := fmt.Sprintf(`SELECT COUNT(DISTINCT c.IdCita) FROM citas c
	          INNER JOIN pxcita px ON px.IdCita = c.IdCita
	          WHERE px.CUPS IN (%s)
	            AND MONTH(c.FeCita) = ?
	            AND YEAR(c.FeCita) = ?
	            AND c.Cancelada = 0 AND c.Remonte = 0`, strings.Join(placeholders, ","))

	args = append(args, month, year)

	var count int
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *AppointmentRepo) FindPendingByDate(ctx context.Context, date string) ([]domain.Appointment, error) {
	query := `SELECT c.IdCita, c.FeCita, c.FechaCita, c.IdMedico,
	            COALESCE(cm.doctor_nombre_completo, c.IdMedico) AS DoctorName,
	            c.NumeroPaciente,
	            COALESCE(p.NCompleto, '') AS PatientName,
	            COALESCE(p.Telefono, '') AS PatientPhone,
	            c.Entidad, c.Agenda, c.Confirmada, c.Observaciones
	          FROM citas c
	          LEFT JOIN cup_medico cm ON cm.doctor_documento = c.IdMedico AND cm.activo = 1
	            AND cm.id = (SELECT MIN(cm2.id) FROM cup_medico cm2 WHERE cm2.doctor_documento = c.IdMedico AND cm2.activo = 1)
	          LEFT JOIN pacientes p ON p.NumeroPaciente = c.NumeroPaciente
	          WHERE c.FeCita = ? AND c.Cancelada = 0 AND c.Remonte = 0 AND c.Confirmada = 0
	          ORDER BY c.FechaCita`

	rows, err := r.db.QueryContext(ctx, query, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var appointments []domain.Appointment
	var ids []string
	for rows.Next() {
		var appt domain.Appointment
		var confirmedInt int
		var observations sql.NullString

		if err := rows.Scan(
			&appt.ID, &appt.Date, &appt.TimeSlot, &appt.DoctorID,
			&appt.DoctorName, &appt.PatientID, &appt.PatientName, &appt.PatientPhone,
			&appt.Entity, &appt.AgendaID, &confirmedInt, &observations,
		); err != nil {
			return nil, err
		}

		appt.Confirmed = (confirmedInt == -1)
		if observations.Valid {
			appt.Observations = observations.String
		}

		appointments = append(appointments, appt)
		ids = append(ids, appt.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		procMap, err := r.fetchProceduresBatch(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range appointments {
			appointments[i].Procedures = procMap[appointments[i].ID]
		}
	}

	return appointments, nil
}

// RescheduleDate moves all non-cancelled appointments for an agenda+doctor+date to a new date.
// Updates FeCita and FechaCita (preserving HHmm time portion via CONCAT+RIGHT) in a single statement.
// Resets confirmation status. Matches Laravel's updateAppointmentsDate().
func (r *AppointmentRepo) RescheduleDate(ctx context.Context, agendaID int, doctorDoc, oldDate, newDate string) (int, error) {
	// "2025-10-15" → "20251015"
	newDateFormatted := strings.ReplaceAll(newDate, "-", "")

	result, err := r.db.ExecContext(ctx,
		`UPDATE citas SET FeCita = ?, FechaCita = CONCAT(?, RIGHT(FechaCita, 4)),
		        Confirmada = 0, FechaConfirmacion = NULL
		 WHERE Agenda = ? AND IdMedico = ? AND FeCita = ?
		   AND Cancelada = 0 AND Remonte = 0`,
		newDate, newDateFormatted, agendaID, doctorDoc, oldDate)
	if err != nil {
		return 0, fmt.Errorf("reschedule date: %w", err)
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}
