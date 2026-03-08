package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/config"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// nowFunc is the clock used by business-hours check. Override in tests.
var nowFunc = time.Now

// RegisterGreetingHandlers registra CHECK_BUSINESS_HOURS, GREETING, MAIN_MENU, OUT_OF_HOURS, OUT_OF_HOURS_MENU
func RegisterGreetingHandlers(m *sm.Machine, cfg *config.Config, locationRepo LocationReader) {
	m.Register(sm.StateCheckBusinessHours, checkBusinessHoursHandler(cfg))
	m.Register(sm.StateOutOfHours, outOfHoursHandler(cfg))
	m.Register(sm.StateOutOfHoursMenu, outOfHoursMenuHandler(cfg, locationRepo))
	m.Register(sm.StateGreeting, greetingHandler(cfg))
	m.RegisterWithConfig(sm.StateMainMenu, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"consultar", "agendar", "resultados", "ubicacion"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, &sm.ListMessage{
				Body:  "En que puedo ayudarte hoy?",
				Title: "Ver opciones",
				Sections: []sm.ListSection{{
					Title: "Menu principal",
					Rows: []sm.ListRow{
						{ID: "agendar", Title: "Agendar cita", Description: "Si buscas una cita como particular o cuentas con una orden de tu IPS"},
						{ID: "consultar", Title: "Citas Programadas", Description: "Si tienes citas programadas"},
						{ID: "resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
						{ID: "ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
					},
				}},
			})
		},
		Handler: mainMenuHandler(),
	})
}

// CHECK_BUSINESS_HOURS (automático)
func checkBusinessHoursHandler(cfg *config.Config) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// Testing bypass: TESTING_ALWAYS_OPEN=true skips business hours check
		if cfg.TestingAlwaysOpen {
			return sm.NewResult(sm.StateGreeting), nil
		}

		now := nowFunc() // Ya en timezone America/Bogota
		weekday := now.Weekday()
		hour := now.Hour()

		inHours := false
		switch weekday {
		case time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday:
			inHours = hour >= 7 && hour < 18
		case time.Saturday:
			inHours = hour >= 7 && hour < 12
		}

		if !inHours {
			return sm.NewResult(sm.StateOutOfHours).
				WithEvent("out_of_hours", map[string]interface{}{
					"day":  weekday.String(),
					"hour": hour,
				}), nil
		}

		return sm.NewResult(sm.StateGreeting), nil
	}
}

// OUT_OF_HOURS (automático) — muestra bienvenida fuera de horario con menú interactivo (2 opciones).
// Bird V2: list with Consultar Resultados + Ubicacion (no agendar/consultar citas fuera de horario).
func outOfHoursHandler(cfg *config.Config) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		welcomeText := fmt.Sprintf("Soy %s, tu asistente virtual de *%s*.\n\n"+
			"Antes de continuar, ten en cuenta que al seguir conversando aceptas "+
			"el tratamiento de tus datos personales conforme a nuestra Politica de "+
			"Proteccion de Datos (Ley 1581 de 2012). disponibles en www.neuroelectrodx.com\n"+
			"Podras ejercer tus derechos de acceso, rectificacion o supresion en cualquier momento.",
			cfg.BotName, cfg.CenterName)

		return sm.NewResult(sm.StateOutOfHoursMenu).
			WithList(welcomeText+"\n\nEn que puedo ayudarte hoy?", "Ver opciones",
				sm.ListSection{
					Title: "Opciones disponibles",
					Rows: []sm.ListRow{
						{ID: "ooh_resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
						{ID: "ooh_ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
					},
				}).
			WithEvent("out_of_hours_menu_shown", nil), nil
	}
}

// OUT_OF_HOURS_MENU (interactivo) — procesa selección del menú fuera de horario.
// Muestra resultados o ubicaciones y termina la conversación.
func outOfHoursMenuHandler(cfg *config.Config, locationRepo LocationReader) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "ooh_resultados", "ooh_ubicacion")
		if result != nil {
			result.Messages = append(result.Messages, &sm.ListMessage{
				Body:  "En que puedo ayudarte hoy?",
				Title: "Ver opciones",
				Sections: []sm.ListSection{{
					Title: "Opciones disponibles",
					Rows: []sm.ListRow{
						{ID: "ooh_resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
						{ID: "ooh_ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
					},
				}},
			})
			return result, nil
		}

		switch selected {
		case "ooh_resultados":
			text := "Ingresa a nuestra pagina: https://neuroelectrodx.com/\n\n" +
				"En el siguiente video encontraras el paso a paso para descargar tus resultados.\n" +
				"https://www.youtube.com/watch?v=kEx51t6OlyQ"
			if cfg.ResultsURL != "" {
				text = fmt.Sprintf("Ingresa a nuestra pagina: %s\n\n"+
					"En el siguiente video encontraras el paso a paso para descargar tus resultados.\n"+
					"https://www.youtube.com/watch?v=kEx51t6OlyQ", cfg.ResultsURL)
			}
			return sm.NewResult(sm.StateTerminated).
				WithText(text).
				WithEvent("ooh_results_shown", nil), nil

		case "ooh_ubicacion":
			text := buildLocationsText(ctx, locationRepo)
			return sm.NewResult(sm.StateTerminated).
				WithText(text).
				WithEvent("ooh_locations_shown", nil), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// buildLocationsText creates location text from DB or fallback.
func buildLocationsText(ctx context.Context, locationRepo LocationReader) string {
	if locationRepo != nil {
		locations, err := locationRepo.FindActive(ctx)
		if err == nil && len(locations) > 0 {
			text := ""
			for i, loc := range locations {
				if i > 0 {
					text += "\n"
				}
				text += fmt.Sprintf("*%s* %s\n", loc.Name, loc.Address)
				if loc.GoogleMapsURL != "" {
					text += loc.GoogleMapsURL + "\n"
				}
				if loc.Phone != "" {
					text += fmt.Sprintf("Tel: %s\n", loc.Phone)
				}
			}
			return text
		}
	}
	return "Actualmente no tenemos sedes configuradas. Comunicate con un agente para mas informacion."
}

// GREETING (automático) — envía bienvenida + lista del menú (4 opciones Bird V2)
func greetingHandler(cfg *config.Config) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		welcomeText := fmt.Sprintf("Soy *%s*, tu asistente virtual de *%s*.\n\n"+
			"Antes de continuar, ten en cuenta que al seguir conversando aceptas "+
			"el tratamiento de tus datos personales conforme a nuestra Politica de "+
			"Proteccion de Datos (Ley 1581 de 2012). disponibles en www.neuroelectrodx.com\n"+
			"Podras ejercer tus derechos de acceso, rectificacion o supresion en cualquier momento.",
			cfg.BotName, cfg.CenterName)

		return sm.NewResult(sm.StateMainMenu).
			WithList(welcomeText+"\n\nEn que puedo ayudarte hoy?", "Ver opciones",
				sm.ListSection{
					Title: "Menu principal",
					Rows: []sm.ListRow{
						{ID: "agendar", Title: "Agendar cita", Description: "Si buscas una cita como particular o cuentas con una orden de tu IPS"},
						{ID: "consultar", Title: "Citas Programadas", Description: "Si tienes citas programadas"},
						{ID: "resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
						{ID: "ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
					},
				}).
			WithEvent("greeting_sent", nil), nil
	}
}

// MAIN_MENU — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func mainMenuHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)

		switch selected {
		case "consultar":
			return sm.NewResult(sm.StateAskDocument).
				WithContext("menu_option", "consultar").
				WithText("Por favor ingresa tu número de documento de identidad (sin puntos ni espacios):").
				WithEvent("menu_selected", map[string]interface{}{"option": "consultar"}), nil
		case "agendar":
			// Bird V2: entity type selection BEFORE document entry
			return sm.NewResult(sm.StateAskClientType).
				WithContext("menu_option", "agendar").
				WithList("Selecciona el tipo de entidad a la que perteneces", "Tipo de entidad",
					sm.ListSection{
						Title: "Tipos de entidad",
						Rows:  buildEntityTypeRows(),
					}).
				WithEvent("menu_selected", map[string]interface{}{"option": "agendar"}), nil
		case "resultados":
			return sm.NewResult(sm.StateShowResults).
				WithEvent("menu_selected", map[string]interface{}{"option": "resultados"}), nil
		case "ubicacion":
			return sm.NewResult(sm.StateShowLocations).
				WithEvent("menu_selected", map[string]interface{}{"option": "ubicacion"}), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// buildEntityTypeRows creates list rows for the 7 entity type options.
func buildEntityTypeRows() []sm.ListRow {
	rows := make([]sm.ListRow, 7)
	for i := 1; i <= 7; i++ {
		rows[i-1] = sm.ListRow{
			ID:          fmt.Sprintf("ct_%d", i),
			Title:       domain.EntityCategoryLabels[i],
			Description: "",
		}
	}
	return rows
}
