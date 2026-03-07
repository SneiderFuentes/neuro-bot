package statemachine

// === Saludo e Identificación ===
const (
	StateCheckBusinessHours = "CHECK_BUSINESS_HOURS"
	StateGreeting           = "GREETING"
	StateMainMenu           = "MAIN_MENU"
	StateAskDocument        = "ASK_DOCUMENT"
	StatePatientLookup      = "PATIENT_LOOKUP"
	StateConfirmIdentity    = "CONFIRM_IDENTITY"
	StateShowResults        = "SHOW_RESULTS"
	StateShowLocations      = "SHOW_LOCATIONS"
)

// === Entity Management ===
const (
	StateCheckEntity     = "CHECK_ENTITY"
	StateConfirmEntity   = "CONFIRM_ENTITY"
	StateChangeEntity    = "CHANGE_ENTITY"
	StateAskClientType   = "ASK_CLIENT_TYPE"
	StateShowEntityList  = "SHOW_ENTITY_LIST"
	StateAskEntityNumber = "ASK_ENTITY_NUMBER"
	StateOutOfHoursMenu  = "OUT_OF_HOURS_MENU"
)

// === Registro de Paciente Nuevo ===
const (
	StateRegistrationStart   = "REGISTRATION_START"
	StateRegDocumentType     = "REG_DOCUMENT_TYPE"
	StateRegFirstSurname     = "REG_FIRST_SURNAME"
	StateRegSecondSurname    = "REG_SECOND_SURNAME"
	StateRegFirstName        = "REG_FIRST_NAME"
	StateRegSecondName       = "REG_SECOND_NAME"
	StateRegBirthDate        = "REG_BIRTH_DATE"
	StateRegBirthPlace       = "REG_BIRTH_PLACE"
	StateRegGender           = "REG_GENDER"
	StateRegMaritalStatus    = "REG_MARITAL_STATUS"
	StateRegAddress          = "REG_ADDRESS"
	StateRegPhone            = "REG_PHONE"
	StateRegPhone2           = "REG_PHONE2"
	StateRegEmail            = "REG_EMAIL"
	StateRegOccupation       = "REG_OCCUPATION"
	StateRegMunicipality     = "REG_MUNICIPALITY"
	StateRegClientType       = "REG_CLIENT_TYPE"
	StateRegUserType         = "REG_USER_TYPE"
	StateRegAffiliationType  = "REG_AFFILIATION_TYPE"
	StateRegEntity           = "REG_ENTITY"
	StateRegZone             = "REG_ZONE"
	StateConfirmRegistration = "CONFIRM_REGISTRATION"
	StateCreatePatient       = "CREATE_PATIENT"
)

// === Consulta de Citas ===
const (
	StateFetchAppointments = "FETCH_APPOINTMENTS"
	StateListAppointments  = "LIST_APPOINTMENTS"
	StateAppointmentAction = "APPOINTMENT_ACTION"
	StateNoAppointments    = "NO_APPOINTMENTS"
)

// === Confirmación / Cancelación de Citas ===
const (
	StateConfirmAppointment   = "CONFIRM_APPOINTMENT"
	StateAppointmentConfirmed = "APPOINTMENT_CONFIRMED"
	StateCancelAppointment    = "CANCEL_APPOINTMENT"
	StateCancelReason         = "CANCEL_REASON"
	StateAppointmentCancelled = "APPOINTMENT_CANCELLED"
)

// === Orden Médica y OCR ===
const (
	StateAskMedicalOrder    = "ASK_MEDICAL_ORDER"
	StateUploadMedicalOrder = "UPLOAD_MEDICAL_ORDER"
	StateValidateOCR        = "VALIDATE_OCR"
	StateConfirmOCRResult   = "CONFIRM_OCR_RESULT"
	StateOCRFailed          = "OCR_FAILED"
	StateAskManualCups      = "ASK_MANUAL_CUPS"
	StateManualProcedure    = "MANUAL_PROCEDURE_INPUT"
	StateSelectProcedure    = "SELECT_PROCEDURE"
)

// === Validaciones Médicas ===
const (
	StateCheckSpecialCups      = "CHECK_SPECIAL_CUPS"
	StateAskGestationalWeeks   = "ASK_GESTATIONAL_WEEKS"
	StateCheckExisting         = "CHECK_EXISTING"
	StateAppointmentExists     = "APPOINTMENT_EXISTS"
	StateAskContrasted         = "ASK_CONTRASTED"
	StateAskPregnancy          = "ASK_PREGNANCY"
	StatePregnancyBlock        = "PREGNANCY_BLOCK"
	StateAskBabyWeight         = "ASK_BABY_WEIGHT"
	StateGfrCreatinine         = "GFR_CREATININE"
	StateGfrHeight             = "GFR_HEIGHT"
	StateGfrWeight             = "GFR_WEIGHT"
	StateGfrDisease            = "GFR_DISEASE"
	StateGfrResult             = "GFR_RESULT"
	StateGfrNotEligible        = "GFR_NOT_ELIGIBLE"
	StateAskSedation           = "ASK_SEDATION"
	StateCheckPriorConsult     = "CHECK_PRIOR_CONSULTATION"
	StateCheckSoatLimit        = "CHECK_SOAT_LIMIT"
	StateCheckAgeRestriction   = "CHECK_AGE_RESTRICTION"
)

// === Búsqueda y Agendamiento ===
const (
	StateSearchSlots       = "SEARCH_SLOTS"
	StateShowSlots         = "SHOW_SLOTS"
	StateNoSlotsAvailable  = "NO_SLOTS_AVAILABLE"
	StateOfferWaitingList  = "OFFER_WAITING_LIST"
	StateConfirmBooking    = "CONFIRM_BOOKING"
	StateCreateAppointment = "CREATE_APPOINTMENT"
	StateBookingSuccess    = "BOOKING_SUCCESS"
	StateBookingFailed     = "BOOKING_FAILED"
)

// === Post-Acción y Cierre ===
const (
	StatePostActionMenu  = "POST_ACTION_MENU"
	StateFallbackMenu    = "FALLBACK_MENU"
	StateChangePatient   = "CHANGE_PATIENT"
	StateFarewell        = "FAREWELL"
	StateTerminated      = "TERMINATED"
	StateOutOfHours      = "OUT_OF_HOURS"
	StateEscalateToAgent = "ESCALATE_TO_AGENT"
	StateEscalated       = "ESCALATED"
)

// StateType indica si un estado es automático o interactivo
type StateType int

const (
	StateTypeAutomatic   StateType = iota // Se ejecuta sin esperar input
	StateTypeInteractive                  // Espera input del usuario
)

// stateTypes define el tipo de cada estado
var stateTypes = map[string]StateType{
	// Saludo e Identificación
	StateCheckBusinessHours: StateTypeAutomatic,
	StateGreeting:           StateTypeAutomatic,
	StateMainMenu:           StateTypeInteractive,
	StateAskDocument:        StateTypeInteractive,
	StatePatientLookup:      StateTypeAutomatic,
	StateConfirmIdentity:    StateTypeInteractive,
	StateShowResults:        StateTypeAutomatic,
	StateShowLocations:      StateTypeAutomatic,

	// Entity Management
	StateCheckEntity:     StateTypeAutomatic,
	StateConfirmEntity:   StateTypeInteractive,
	StateChangeEntity:    StateTypeInteractive,
	StateAskClientType:   StateTypeInteractive,
	StateShowEntityList:  StateTypeAutomatic,
	StateAskEntityNumber: StateTypeInteractive,
	StateOutOfHoursMenu:  StateTypeInteractive,

	// Registro
	StateRegistrationStart:   StateTypeInteractive,
	StateRegDocumentType:     StateTypeInteractive,
	StateRegFirstSurname:     StateTypeInteractive,
	StateRegSecondSurname:    StateTypeInteractive,
	StateRegFirstName:        StateTypeInteractive,
	StateRegSecondName:       StateTypeInteractive,
	StateRegBirthDate:        StateTypeInteractive,
	StateRegBirthPlace:       StateTypeInteractive,
	StateRegGender:           StateTypeInteractive,
	StateRegMaritalStatus:    StateTypeInteractive,
	StateRegAddress:          StateTypeInteractive,
	StateRegPhone:            StateTypeInteractive,
	StateRegPhone2:           StateTypeInteractive,
	StateRegEmail:            StateTypeInteractive,
	StateRegOccupation:       StateTypeInteractive,
	StateRegMunicipality:     StateTypeInteractive,
	StateRegClientType:       StateTypeInteractive,
	StateRegUserType:         StateTypeInteractive,
	StateRegAffiliationType:  StateTypeInteractive,
	StateRegEntity:           StateTypeInteractive,
	StateRegZone:             StateTypeInteractive,
	StateConfirmRegistration: StateTypeInteractive,
	StateCreatePatient:       StateTypeAutomatic,

	// Consulta de Citas
	StateFetchAppointments: StateTypeAutomatic,
	StateListAppointments:  StateTypeInteractive,
	StateAppointmentAction: StateTypeInteractive,
	StateNoAppointments:    StateTypeAutomatic,

	// Confirmación / Cancelación
	StateConfirmAppointment:   StateTypeInteractive,
	StateAppointmentConfirmed: StateTypeAutomatic,
	StateCancelAppointment:    StateTypeInteractive,
	StateCancelReason:         StateTypeInteractive,
	StateAppointmentCancelled: StateTypeAutomatic,

	// Orden Médica y OCR
	StateAskMedicalOrder:    StateTypeInteractive,
	StateUploadMedicalOrder: StateTypeInteractive,
	StateValidateOCR:        StateTypeAutomatic,
	StateConfirmOCRResult:   StateTypeInteractive,
	StateOCRFailed:          StateTypeAutomatic,
	StateAskManualCups:      StateTypeInteractive,
	StateManualProcedure:    StateTypeInteractive,
	StateSelectProcedure:    StateTypeInteractive,

	// Validaciones Médicas
	StateCheckSpecialCups:    StateTypeAutomatic,
	StateAskGestationalWeeks: StateTypeInteractive,
	StateCheckExisting:       StateTypeAutomatic,
	StateAppointmentExists:   StateTypeAutomatic,
	StateAskContrasted:       StateTypeAutomatic, // Auto-skips if not contrastable; prompts if contrastable
	StateAskPregnancy:        StateTypeAutomatic, // Auto-skips for males and babies; prompts for females >= 1
	StatePregnancyBlock:      StateTypeAutomatic,
	StateAskBabyWeight:       StateTypeInteractive,
	StateGfrCreatinine:       StateTypeInteractive,
	StateGfrHeight:           StateTypeInteractive,
	StateGfrWeight:           StateTypeInteractive,
	StateGfrDisease:          StateTypeInteractive,
	StateGfrResult:           StateTypeAutomatic,
	StateGfrNotEligible:      StateTypeAutomatic,
	StateAskSedation:         StateTypeAutomatic, // Auto-skips if not sedatable; prompts if sedatable
	StateCheckPriorConsult:   StateTypeAutomatic,
	StateCheckSoatLimit:      StateTypeAutomatic,
	StateCheckAgeRestriction: StateTypeAutomatic,

	// Búsqueda y Agendamiento
	StateSearchSlots:       StateTypeAutomatic,
	StateShowSlots:         StateTypeInteractive,
	StateNoSlotsAvailable:  StateTypeAutomatic,
	StateOfferWaitingList:  StateTypeInteractive,
	StateConfirmBooking:    StateTypeInteractive,
	StateCreateAppointment: StateTypeAutomatic,
	StateBookingSuccess:    StateTypeAutomatic,
	StateBookingFailed:     StateTypeAutomatic,

	// Post-Acción y Cierre
	StatePostActionMenu:  StateTypeInteractive,
	StateFallbackMenu:    StateTypeInteractive,
	StateChangePatient:   StateTypeAutomatic,
	StateFarewell:        StateTypeAutomatic,
	StateTerminated:      StateTypeAutomatic,
	StateOutOfHours:      StateTypeAutomatic,
	StateEscalateToAgent: StateTypeAutomatic,
	StateEscalated:       StateTypeAutomatic,
}

// IsAutomatic retorna true si el estado es automático.
// Unknown states default to Interactive (safe: waits for user input).
func IsAutomatic(state string) bool {
	st, ok := stateTypes[state]
	if !ok {
		return false // Unknown states are treated as Interactive
	}
	return st == StateTypeAutomatic
}
