package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/statemachine/validators"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// RegisterRegistrationHandlers registra todos los handlers del flujo de registro de paciente nuevo.
func RegisterRegistrationHandlers(
	m *sm.Machine,
	patientSvc *services.PatientService,
	municipalityRepo repository.MunicipalityRepository,
	entityRepo repository.EntityRepository,
) {
	m.RegisterWithConfig(sm.StateRegistrationStart, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"register_yes", "register_no"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: "¿Deseas registrarte?",
				Buttons: []sm.Button{
					{Text: "Sí, registrarme", Payload: "register_yes"},
					{Text: "No, gracias", Payload: "register_no"},
				},
			})
		},
		Handler: registrationStartHandler(),
	})
	m.RegisterWithConfig(sm.StateRegDocumentType, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"CC", "TI", "CE", "PA", "RC", "MS", "AS"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ListMessage{
				Body: "Tipo de documento", Title: "Seleccionar",
				Sections: []sm.ListSection{{
					Title: "Tipos de documento",
					Rows: []sm.ListRow{
						{ID: "CC", Title: "CC - Cédula de Ciudadanía"},
						{ID: "TI", Title: "TI - Tarjeta de Identidad"},
						{ID: "CE", Title: "CE - Cédula de Extranjería"},
						{ID: "PA", Title: "PA - Pasaporte"},
						{ID: "RC", Title: "RC - Registro Civil"},
						{ID: "MS", Title: "MS - Menor sin ID"},
						{ID: "AS", Title: "AS - Adulto sin ID"},
					},
				}},
			})
		},
		Handler: regDocumentTypeHandler(),
	})
	m.Register(sm.StateRegFirstSurname, regFieldHandler("reg_first_surname", "Por favor escribe tu primer apellido (solo letras, sin números, ni símbolos ni espacios).", validateName, sm.StateRegSecondSurname))
	m.Register(sm.StateRegSecondSurname, regOptionalFieldHandler("reg_second_surname", "Si tienes segundo apellido, escríbelo. Si no, responde \"NA\".", sm.StateRegFirstName))
	m.Register(sm.StateRegFirstName, regFieldHandler("reg_first_name", "Por favor escribe tu primer nombre (solo letras, sin números, ni símbolos ni espacios).", validateName, sm.StateRegSecondName))
	m.Register(sm.StateRegSecondName, regOptionalFieldHandler("reg_second_name", "Si tienes segundo nombre, escríbelo. Si no, responde \"NA\".", sm.StateRegBirthDate))
	m.Register(sm.StateRegBirthDate, regBirthDateHandler())
	m.Register(sm.StateRegBirthPlace, regBirthPlaceHandler())
	m.RegisterWithConfig(sm.StateRegGender, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"M", "F"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text:    "Selecciona tu género:",
				Buttons: []sm.Button{{Text: "Masculino", Payload: "M"}, {Text: "Femenino", Payload: "F"}},
			})
		},
		Handler: regGenderHandler(),
	})
	m.RegisterWithConfig(sm.StateRegMaritalStatus, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"SOLTERO", "CASADO", "UNION LIBRE", "DIVORCIADO", "VIUDO"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ListMessage{
				Body: "Estado civil", Title: "Seleccionar",
				Sections: []sm.ListSection{{Title: "Estado civil", Rows: []sm.ListRow{
					{ID: "SOLTERO", Title: "Soltero/a"},
					{ID: "CASADO", Title: "Casado/a"},
					{ID: "UNION LIBRE", Title: "Unión libre"},
					{ID: "DIVORCIADO", Title: "Divorciado/a"},
					{ID: "VIUDO", Title: "Viudo/a"},
				}}},
			})
		},
		Handler: regMaritalStatusHandler(),
	})
	m.Register(sm.StateRegAddress, regFieldHandler("reg_address", "Escribe tu dirección completa (calle, número, barrio).", validateNotEmpty, sm.StateRegPhone))
	m.Register(sm.StateRegPhone, regPhoneHandler())
	m.Register(sm.StateRegPhone2, regOptionalPhoneHandler())
	m.Register(sm.StateRegEmail, regEmailHandler())
	m.Register(sm.StateRegOccupation, regFieldHandler("reg_occupation", "Indica tu ocupación (ej.: Empleado, Estudiante).", validateNotEmpty, sm.StateRegMunicipality))
	m.Register(sm.StateRegMunicipality, regMunicipalityHandler(municipalityRepo))
	m.RegisterWithConfig(sm.StateRegZone, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"U", "R"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text:    "Selecciona tu zona:",
				Buttons: []sm.Button{{Text: "Urbana", Payload: "U"}, {Text: "Rural", Payload: "R"}},
			})
		},
		Handler: regZoneHandler(),
	})
	m.RegisterWithConfig(sm.StateRegClientType, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"PARTICULAR", "EPS", "SOAT"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: "Selecciona tu tipo de cliente:",
				Buttons: []sm.Button{
					{Text: "Particular", Payload: "PARTICULAR"},
					{Text: "EPS", Payload: "EPS"},
					{Text: "SOAT", Payload: "SOAT"},
				},
			})
		},
		Handler: regClientTypeHandler(),
	})
	m.RegisterWithConfig(sm.StateRegUserType, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"CONTRIBUTIVO", "SUBSIDIADO", "PARTICULAR"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: "Selecciona tu tipo de usuario:",
				Buttons: []sm.Button{
					{Text: "Contributivo", Payload: "CONTRIBUTIVO"},
					{Text: "Subsidiado", Payload: "SUBSIDIADO"},
					{Text: "Particular", Payload: "PARTICULAR"},
				},
			})
		},
		Handler: regUserTypeHandler(),
	})
	m.RegisterWithConfig(sm.StateRegAffiliationType, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"COTIZANTE", "BENEFICIARIO", "OTRO"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: "Selecciona tu tipo de afiliación:",
				Buttons: []sm.Button{
					{Text: "Cotizante", Payload: "COTIZANTE"},
					{Text: "Beneficiario", Payload: "BENEFICIARIO"},
					{Text: "Otro", Payload: "OTRO"},
				},
			})
		},
		Handler: regAffiliationTypeHandler(),
	})
	m.Register(sm.StateRegEntity, regEntityHandler(entityRepo))
	m.RegisterWithConfig(sm.StateConfirmRegistration, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"reg_confirm", "reg_correct"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: buildRegistrationSummary(sess),
				Buttons: []sm.Button{
					{Text: "✅ Sí, confirmar", Payload: "reg_confirm"},
					{Text: "✏️ Corregir datos", Payload: "reg_correct"},
				},
			})
		},
		Handler: confirmRegistrationHandler(),
	})
	m.Register(sm.StateCreatePatient, createPatientHandler(patientSvc))
}

// --- Validaciones reutilizables (delegadas al paquete validators) ---

var validateName = validators.Name

func validateNotEmpty(s string) bool {
	return len(strings.TrimSpace(s)) > 0
}

var noResponses = map[string]bool{
	"no tengo": true, "no": true, "ninguno": true, "ninguna": true, "n/a": true, "na": true, "-": true,
}

// --- Handlers genéricos ---

// regFieldHandler crea un handler para campos de texto con validación.
// El handler anterior ya envió el prompt; este solo procesa la respuesta.
func regFieldHandler(ctxKey, prompt string, validate func(string) bool, nextState string) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		// Reject internal trigger texts (e.g. __resume__) so they are
		// never stored as real patient data.
		if sm.IsReservedKeyword(input) {
			input = ""
		}

		retryResult := sm.ValidateWithRetry(sess, input, validate, "Respuesta no válida. "+prompt)
		if retryResult != nil {
			return retryResult, nil
		}

		return sm.NewResult(nextState).
			WithContext(ctxKey, strings.ToUpper(input)), nil
	}
}

// regOptionalFieldHandler crea un handler para campos opcionales.
func regOptionalFieldHandler(ctxKey, prompt string, nextState string) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		if noResponses[strings.ToLower(input)] {
			return sm.NewResult(nextState).
				WithContext(ctxKey, ""), nil
		}

		if !validateName(input) {
			retryResult := sm.ValidateWithRetry(sess, input, validateName, "Respuesta no válida. "+prompt)
			if retryResult != nil {
				return retryResult, nil
			}
		}

		return sm.NewResult(nextState).
			WithContext(ctxKey, strings.ToUpper(input)), nil
	}
}

// --- Handlers especializados ---

// REGISTRATION_START — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func registrationStartHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		switch selected {
		case "register_yes":
			return sm.NewResult(sm.StateRegDocumentType).
				WithList("¡Perfecto! Vamos a registrarte. Selecciona tu *tipo de documento*:", "Seleccionar",
					sm.ListSection{
						Title: "Tipos de documento",
						Rows: []sm.ListRow{
							{ID: "CC", Title: "CC - Cédula de Ciudadanía"},
							{ID: "TI", Title: "TI - Tarjeta de Identidad"},
							{ID: "CE", Title: "CE - Cédula de Extranjería"},
							{ID: "PA", Title: "PA - Pasaporte"},
							{ID: "RC", Title: "RC - Registro Civil"},
							{ID: "MS", Title: "MS - Menor sin ID"},
							{ID: "AS", Title: "AS - Adulto sin ID"},
						},
					}).
				WithEvent("registration_started", nil), nil
		case "register_no":
			return sm.NewResult(sm.StatePostActionMenu).
				WithButtons("Entendido. Si necesitas algo más, estoy aquí para ayudarte.\n\n¿Qué deseas hacer?",
					sm.Button{Text: "📅 Agendar otra cita", Payload: "otra_cita"},
					sm.Button{Text: "📋 Ver mis citas", Payload: "ver_citas"},
					sm.Button{Text: "🔄 Cambiar paciente", Payload: "cambiar_paciente"},
				).
				WithEvent("registration_declined", nil), nil
		}
		return nil, fmt.Errorf("unreachable")
	}
}

// REG_DOCUMENT_TYPE — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regDocumentTypeHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegFirstSurname).
			WithContext("reg_document_type", selected).
			WithText("Por favor escribe tu primer apellido (solo letras, sin números, ni símbolos ni espacios)."), nil
	}
}

// REG_BIRTH_DATE (interactivo) — fecha de nacimiento AAAA-MM-DD
func regBirthDateHandler() sm.StateHandler {
	formats := []string{"2006-01-02", "2006/01/02"}

	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		var parsedDate time.Time
		var parseErr error
		for _, format := range formats {
			parsedDate, parseErr = time.Parse(format, input)
			if parseErr == nil {
				break
			}
		}

		if parseErr != nil {
			retryResult := sm.ValidateWithRetry(sess, "", func(string) bool { return false },
				"Formato de fecha no válido. Ingresa tu fecha de nacimiento en formato *AAAA-MM-DD* (ejemplo: 1992-04-17).")
			return retryResult, nil
		}

		now := time.Now()
		if parsedDate.After(now) {
			return sm.NewResult(sess.CurrentState).
				WithText("La fecha no puede ser en el futuro. Ingresa tu fecha de nacimiento real en formato *AAAA-MM-DD*:"), nil
		}
		if parsedDate.Year() < 1900 {
			return sm.NewResult(sess.CurrentState).
				WithText("Fecha no válida. Ingresa tu fecha de nacimiento en formato *AAAA-MM-DD*:"), nil
		}

		age := services.CalculateAge(parsedDate)
		sess.RetryCount = 0

		return sm.NewResult(sm.StateRegBirthPlace).
			WithContext("reg_birth_date", parsedDate.Format("2006-01-02")).
			WithContext("patient_age", fmt.Sprintf("%d", age)).
			WithText("Ciudad de nacimiento (ej.: Villavicencio):"), nil
	}
}

// REG_BIRTH_PLACE (interactivo) — lugar de nacimiento
func regBirthPlaceHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		// Reject internal trigger texts (e.g. __resume__)
		if sm.IsReservedKeyword(input) {
			input = ""
		}

		retryResult := sm.ValidateWithRetry(sess, input, validateNotEmpty, "Ciudad de nacimiento (ej.: Villavicencio):")
		if retryResult != nil {
			return retryResult, nil
		}

		return sm.NewResult(sm.StateRegGender).
			WithContext("reg_birth_place", strings.ToUpper(input)).
			WithButtons("Selecciona tu *género*:",
				sm.Button{Text: "Masculino", Payload: "M"},
				sm.Button{Text: "Femenino", Payload: "F"},
			), nil
	}
}

// REG_GENDER — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regGenderHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegMaritalStatus).
			WithContext("reg_gender", selected).
			WithContext("patient_gender", selected).
			WithList("Selecciona tu *estado civil*:", "Seleccionar",
				sm.ListSection{
					Title: "Estado civil",
					Rows: []sm.ListRow{
						{ID: "SOLTERO", Title: "Soltero/a"},
						{ID: "CASADO", Title: "Casado/a"},
						{ID: "UNION LIBRE", Title: "Unión libre"},
						{ID: "DIVORCIADO", Title: "Divorciado/a"},
						{ID: "VIUDO", Title: "Viudo/a"},
					},
				}), nil
	}
}

// REG_MARITAL_STATUS — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regMaritalStatusHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegAddress).
			WithContext("reg_marital_status", selected).
			WithText("Escribe tu dirección completa (calle, número, barrio):"), nil
	}
}

// REG_PHONE (interactivo) — teléfono principal colombiano
func regPhoneHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		parsed := utils.ParseColombianPhone(input)
		if parsed == "" {
			retryResult := sm.ValidateWithRetry(sess, "", func(string) bool { return false },
				"Número no válido. Ingresa tu celular principal preferiblemente con WhatsApp (ej: 3001234567).")
			return retryResult, nil
		}

		sess.RetryCount = 0
		return sm.NewResult(sm.StateRegPhone2).
			WithContext("reg_phone", parsed).
			WithContext("patient_phone", parsed).
			WithText("Ingresa un número de celular secundario (ej: 3001234567) o responde \"NA\":"), nil
	}
}

// REG_PHONE2 (interactivo) — teléfono secundario opcional
func regOptionalPhoneHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		if noResponses[strings.ToLower(input)] {
			return sm.NewResult(sm.StateRegEmail).
				WithContext("reg_phone2", "").
				WithText("Indica tu correo electrónico (sin espacios y en minúscula) o responde \"NA\":"), nil
		}

		parsed := utils.ParseColombianPhone(input)
		if parsed == "" {
			// No es un teléfono válido, pero es campo opcional, guardar vacío
			return sm.NewResult(sm.StateRegEmail).
				WithContext("reg_phone2", "").
				WithText("Indica tu correo electrónico (sin espacios y en minúscula) o responde \"NA\":"), nil
		}

		return sm.NewResult(sm.StateRegEmail).
			WithContext("reg_phone2", parsed).
			WithText("Indica tu correo electrónico (sin espacios y en minúscula) o responde \"NA\":"), nil
	}
}

// REG_EMAIL (interactivo) — email o "no tengo"
func regEmailHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)
		lower := strings.ToLower(input)

		if noResponses[lower] {
			return sm.NewResult(sm.StateRegOccupation).
				WithContext("reg_email", "").
				WithText("Indica tu ocupación (ej.: Empleado, Estudiante):"), nil
		}

		if !validators.Email(lower) {
			retryResult := sm.ValidateWithRetry(sess, "", func(string) bool { return false },
				"Email no válido. Indica tu correo electrónico (sin espacios y en minúscula) o responde \"NA\".")
			return retryResult, nil
		}

		sess.RetryCount = 0
		return sm.NewResult(sm.StateRegOccupation).
			WithContext("reg_email", lower).
			WithText("Indica tu ocupación (ej.: Empleado, Estudiante):"), nil
	}
}

// REG_MUNICIPALITY (interactivo) — búsqueda fuzzy de municipios
func regMunicipalityHandler(municipalityRepo repository.MunicipalityRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// Selección desde lista (postback con código de municipio)
		if msg.IsPostback {
			sess.RetryCount = 0
			return sm.NewResult(sm.StateRegZone).
				WithContext("reg_municipality", msg.PostbackPayload).
				WithButtons("Selecciona tu *zona*:",
					sm.Button{Text: "Urbana", Payload: "U"},
					sm.Button{Text: "Rural", Payload: "R"},
				), nil
		}

		input := strings.TrimSpace(msg.Text)
		if input == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Nombre completo de tu municipio y departamento de residencia. Ejemplo: Villavicencio - Meta:"), nil
		}

		results, err := municipalityRepo.Search(ctx, input)
		if err != nil {
			return sm.NewResult(sess.CurrentState).
				WithText("Error al buscar municipios. Intenta de nuevo escribiendo el nombre de tu municipio:"), nil
		}

		outcome, errResult := sm.ValidateSearchCount(sess, len(results), 5,
			"No encontré municipios con ese nombre. Intenta con otro nombre o sé más específico:",
			"Encontré demasiados resultados. Sé más específico (ejemplo: escribe \"Villavicencio\" en lugar de \"Villa\"):")
		if errResult != nil {
			return errResult, nil
		}

		switch outcome {
		case sm.SearchExact:
			sess.RetryCount = 0
			return sm.NewResult(sm.StateRegZone).
				WithContext("reg_municipality", results[0].MunicipalityCode).
				WithButtons(fmt.Sprintf("Municipio seleccionado: *%s (%s)*\n\nSelecciona tu *zona*:", results[0].MunicipalityName, results[0].DepartmentName),
					sm.Button{Text: "Urbana", Payload: "U"},
					sm.Button{Text: "Rural", Payload: "R"},
				), nil
		default: // SearchMultiple
			rows := make([]sm.ListRow, len(results))
			for i, r := range results {
				rows[i] = sm.ListRow{
					ID:          r.MunicipalityCode,
					Title:       r.MunicipalityName,
					Description: r.DepartmentName,
				}
			}
			return sm.NewResult(sess.CurrentState).
				WithList("Selecciona tu municipio:", "Municipios",
					sm.ListSection{Title: "Resultados", Rows: rows}), nil
		}
	}
}

// REG_ZONE — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regZoneHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegClientType).
			WithContext("reg_zone", selected).
			WithButtons("¿Cuál es tu *tipo de cliente*?",
				sm.Button{Text: "Particular", Payload: "PARTICULAR"},
				sm.Button{Text: "EPS", Payload: "EPS"},
				sm.Button{Text: "SOAT", Payload: "SOAT"},
			), nil
	}
}

// REG_CLIENT_TYPE — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regClientTypeHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegUserType).
			WithContext("reg_client_type", selected).
			WithButtons("¿Cuál es tu *tipo de usuario*?",
				sm.Button{Text: "Contributivo", Payload: "CONTRIBUTIVO"},
				sm.Button{Text: "Subsidiado", Payload: "SUBSIDIADO"},
				sm.Button{Text: "Particular", Payload: "PARTICULAR"},
			), nil
	}
}

// REG_USER_TYPE — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regUserTypeHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegAffiliationType).
			WithContext("reg_user_type", selected).
			WithButtons("¿Cuál es tu *tipo de afiliación*?",
				sm.Button{Text: "Cotizante", Payload: "COTIZANTE"},
				sm.Button{Text: "Beneficiario", Payload: "BENEFICIARIO"},
				sm.Button{Text: "Otro", Payload: "OTRO"},
			), nil
	}
}

// REG_AFFILIATION_TYPE — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func regAffiliationTypeHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		return sm.NewResult(sm.StateRegEntity).
			WithContext("reg_affiliation_type", selected).
			WithText("Escribe el nombre de tu *entidad o EPS* (ejemplo: Nueva EPS, Sanitas, etc.):"), nil
	}
}

// REG_ENTITY (interactivo) — búsqueda/selección de entidad
func regEntityHandler(entityRepo repository.EntityRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// Selección desde lista (postback con código de entidad)
		if msg.IsPostback {
			sess.RetryCount = 0
			return sm.NewResult(sm.StateConfirmRegistration).
				WithContext("reg_entity", msg.PostbackPayload).
				WithContext("patient_entity", msg.PostbackPayload), nil
		}

		input := strings.TrimSpace(msg.Text)
		if input == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Escribe el nombre de tu *entidad o EPS*:"), nil
		}

		entities, err := entityRepo.FindActive(ctx)
		if err != nil {
			return sm.NewResult(sess.CurrentState).
				WithText("Error al buscar entidades. Intenta de nuevo:"), nil
		}

		// Filtrar por coincidencia parcial
		inputLower := strings.ToLower(input)
		var matches []domain.Entity
		for _, e := range entities {
			if strings.Contains(strings.ToLower(e.Name), inputLower) ||
				strings.EqualFold(e.Code, input) {
				matches = append(matches, e)
			}
		}

		outcome, errResult := sm.ValidateSearchCount(sess, len(matches), 10,
			"No encontré entidades con ese nombre. Intenta con otro nombre (ejemplo: Nueva EPS, Sanitas, Sura, etc.):",
			"Encontré demasiados resultados. Sé más específico con el nombre de tu entidad:")
		if errResult != nil {
			return errResult, nil
		}

		switch outcome {
		case sm.SearchExact:
			sess.RetryCount = 0
			return sm.NewResult(sm.StateConfirmRegistration).
				WithContext("reg_entity", matches[0].Code).
				WithContext("patient_entity", matches[0].Code).
				WithText(fmt.Sprintf("Entidad seleccionada: *%s*", matches[0].Name)), nil
		default: // SearchMultiple
			rows := make([]sm.ListRow, len(matches))
			for i, e := range matches {
				rows[i] = sm.ListRow{
					ID:    e.Code,
					Title: e.Name,
				}
			}
			return sm.NewResult(sess.CurrentState).
				WithList("Selecciona tu entidad:", "Entidades",
					sm.ListSection{Title: "Entidades activas", Rows: rows}), nil
		}
	}
}

// CONFIRM_REGISTRATION — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func confirmRegistrationHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		switch selected {
		case "reg_confirm":
			return sm.NewResult(sm.StateCreatePatient).
				WithEvent("registration_confirmed", nil), nil
		case "reg_correct":
			return sm.NewResult(sm.StateRegDocumentType).
				WithList("Vamos a corregir tus datos. Comencemos de nuevo.\n\nSelecciona tu *tipo de documento*:", "Seleccionar",
					sm.ListSection{
						Title: "Tipos de documento",
						Rows: []sm.ListRow{
							{ID: "CC", Title: "CC - Cédula de Ciudadanía"},
							{ID: "TI", Title: "TI - Tarjeta de Identidad"},
							{ID: "CE", Title: "CE - Cédula de Extranjería"},
							{ID: "PA", Title: "PA - Pasaporte"},
							{ID: "RC", Title: "RC - Registro Civil"},
							{ID: "MS", Title: "MS - Menor sin ID"},
							{ID: "AS", Title: "AS - Adulto sin ID"},
						},
					}).
				WithEvent("registration_corrected", nil), nil
		}
		return nil, fmt.Errorf("unreachable")
	}
}

// CREATE_PATIENT (automático) — crea paciente en BD externa
func createPatientHandler(patientSvc *services.PatientService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		birthDate, _ := time.Parse("2006-01-02", sess.GetContext("reg_birth_date"))

		input := domain.CreatePatientInput{
			DocumentType:    sess.GetContext("reg_document_type"),
			DocumentNumber:  sess.GetContext("patient_doc"),
			FirstName:       sess.GetContext("reg_first_name"),
			SecondName:      sess.GetContext("reg_second_name"),
			FirstSurname:    sess.GetContext("reg_first_surname"),
			SecondSurname:   sess.GetContext("reg_second_surname"),
			BirthDate:       birthDate,
			BirthPlace:      sess.GetContext("reg_birth_place"),
			Gender:          sess.GetContext("reg_gender"),
			Phone:           sess.GetContext("reg_phone"),
			Email:           sess.GetContext("reg_email"),
			Address:         sess.GetContext("reg_address"),
			CityCode:        sess.GetContext("reg_municipality"),
			Zone:            sess.GetContext("reg_zone"),
			EntityCode:      sess.GetContext("reg_entity"),
			AffiliationType: sess.GetContext("reg_affiliation_type"),
			UserType:        sess.GetContext("reg_user_type"),
			Occupation:      sess.GetContext("reg_occupation"),
			MaritalStatus:   sess.GetContext("reg_marital_status"),
			CountryCode:     "170",
		}

		patientID, err := patientSvc.Create(ctx, input)
		if err != nil {
			return sm.NewResult(sm.StatePostActionMenu).
				WithButtons("Lo siento, hubo un error al crear tu registro. Por favor intenta más tarde o contacta a un agente.\n\n¿Qué deseas hacer?",
					sm.Button{Text: "📅 Agendar otra cita", Payload: "otra_cita"},
					sm.Button{Text: "📋 Ver mis citas", Payload: "ver_citas"},
					sm.Button{Text: "🔄 Cambiar paciente", Payload: "cambiar_paciente"},
				).
				WithEvent("registration_failed", map[string]interface{}{"error": err.Error()}), nil
		}

		// Construir nombre completo para la sesión
		nameParts := []string{}
		for _, p := range []string{
			sess.GetContext("reg_first_name"),
			sess.GetContext("reg_second_name"),
			sess.GetContext("reg_first_surname"),
			sess.GetContext("reg_second_surname"),
		} {
			if strings.TrimSpace(p) != "" {
				nameParts = append(nameParts, p)
			}
		}
		fullName := strings.Join(nameParts, " ")

		// Guardar datos del paciente en sesión
		sess.PatientID = patientID
		sess.PatientDoc = sess.GetContext("patient_doc")
		sess.PatientName = fullName
		sess.PatientEntity = sess.GetContext("reg_entity")

		return sm.NewResult(sm.StateAskMedicalOrder).
			WithContext("patient_id", patientID).
			WithContext("patient_name", fullName).
			WithText(fmt.Sprintf("✅ *¡Registro exitoso!*\n\nBienvenido/a *%s*. Ahora procedamos a agendar tu cita.", fullName)).
			WithEvent("registration_success", map[string]interface{}{"patient_id": patientID}), nil
	}
}

// --- Helpers ---

func buildRegistrationSummary(sess *session.Session) string {
	return fmt.Sprintf("📋 *Resumen de tu registro:*\n\n"+
		"🆔 Documento: %s %s\n"+
		"👤 Nombre: %s %s %s %s\n"+
		"📅 Nacimiento: %s (Edad: %s)\n"+
		"🏙 Lugar de nacimiento: %s\n"+
		"⚧ Género: %s\n"+
		"💍 Estado civil: %s\n"+
		"🏠 Dirección: %s\n"+
		"📱 Teléfono: %s\n"+
		"📧 Email: %s\n"+
		"💼 Ocupación: %s\n"+
		"🏙 Municipio: %s\n"+
		"🏥 Entidad: %s\n\n"+
		"¿Los datos son correctos?",
		sess.GetContext("reg_document_type"), sess.GetContext("patient_doc"),
		sess.GetContext("reg_first_name"), sess.GetContext("reg_second_name"),
		sess.GetContext("reg_first_surname"), sess.GetContext("reg_second_surname"),
		sess.GetContext("reg_birth_date"), sess.GetContext("patient_age"),
		formatOptional(sess.GetContext("reg_birth_place")),
		formatGender(sess.GetContext("reg_gender")),
		sess.GetContext("reg_marital_status"),
		sess.GetContext("reg_address"),
		sess.GetContext("reg_phone"),
		formatOptional(sess.GetContext("reg_email")),
		sess.GetContext("reg_occupation"),
		sess.GetContext("reg_municipality"),
		sess.GetContext("reg_entity"),
	)
}

func formatGender(g string) string {
	switch g {
	case "M":
		return "Masculino"
	case "F":
		return "Femenino"
	default:
		return g
	}
}

func formatOptional(s string) string {
	if s == "" {
		return "No tiene"
	}
	return s
}
