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
		Options:   []string{"consultar", "agendar", "resultados", "ubicacion", "ayuda"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			list := buildMainMenuList()
			list.Body = "Por favor selecciona una opcion del menu.\n\n" + list.Body
			result.Messages = append(result.Messages, list)
		},
		Handler: mainMenuHandler(),
	})
	m.Register(sm.StateShowHelp, showHelpHandler())
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

// OUT_OF_HOURS (automático) — muestra bienvenida fuera de horario con menú interactivo (3 opciones).
// Bird V2: list with Consultar Resultados + Ubicacion + Ayuda (no agendar/consultar citas fuera de horario).
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
						{ID: "ooh_ayuda", Title: "Como usar el bot", Description: "Guia rapida de como interactuar conmigo"},
					},
				}).
			WithEvent("out_of_hours_menu_shown", nil), nil
	}
}

// OUT_OF_HOURS_MENU (interactivo) — procesa selección del menú fuera de horario.
// Muestra resultados, ubicaciones o ayuda y termina la conversación (excepto ayuda que vuelve al menú OOH).
func outOfHoursMenuHandler(cfg *config.Config, locationRepo LocationReader) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "ooh_resultados", "ooh_ubicacion", "ooh_ayuda")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			// Clear default error text — combine into list body (1 message)
			result.Messages = nil
			result.Messages = append(result.Messages, &sm.ListMessage{
				Body:  "Por favor selecciona una opcion.\n\nEn que puedo ayudarte hoy?",
				Title: "Ver opciones",
				Sections: []sm.ListSection{{
					Title: "Opciones disponibles",
					Rows: []sm.ListRow{
						{ID: "ooh_resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
						{ID: "ooh_ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
						{ID: "ooh_ayuda", Title: "Como usar el bot", Description: "Guia rapida de como interactuar conmigo"},
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

		case "ooh_ayuda":
			return sm.NewResult(sm.StateShowHelp).
				WithContext("help_source", "ooh").
				WithEvent("ooh_help_selected", nil), nil
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

// GREETING (automático) — envía bienvenida + lista del menú (5 opciones Bird V2)
func greetingHandler(cfg *config.Config) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		welcomeText := fmt.Sprintf("Soy *%s*, tu asistente virtual de *%s*.\n\n"+
			"Antes de continuar, ten en cuenta que al seguir conversando aceptas "+
			"el tratamiento de tus datos personales conforme a nuestra Politica de "+
			"Proteccion de Datos (Ley 1581 de 2012). disponibles en www.neuroelectrodx.com\n"+
			"Podras ejercer tus derechos de acceso, rectificacion o supresion en cualquier momento.",
			cfg.BotName, cfg.CenterName)

		r := sm.NewResult(sm.StateMainMenu).
			WithEvent("greeting_sent", nil)
		list := buildMainMenuList()
		list.Body = welcomeText + "\n\n" + list.Body
		r.Messages = append(r.Messages, list)
		return r, nil
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
		case "ayuda":
			return sm.NewResult(sm.StateShowHelp).
				WithEvent("menu_selected", map[string]interface{}{"option": "ayuda"}), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// buildMainMenuList creates the main menu list message with 5 options.
func buildMainMenuList() *sm.ListMessage {
	return &sm.ListMessage{
		Body:  "En que puedo ayudarte hoy?",
		Title: "Ver opciones",
		Sections: []sm.ListSection{{
			Title: "Menu principal",
			Rows: []sm.ListRow{
				{ID: "agendar", Title: "Agendar cita", Description: "Si buscas una cita como particular o cuentas con una orden de tu IPS"},
				{ID: "consultar", Title: "Citas Programadas", Description: "Si tienes citas programadas"},
				{ID: "resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
				{ID: "ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
				{ID: "ayuda", Title: "Como usar el bot", Description: "Guia rapida de como interactuar conmigo"},
			},
		}},
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

// SHOW_HELP (automático) — muestra guia de uso del bot y vuelve al menu correspondiente.
// Si viene del menú fuera de horario (help_source=ooh), vuelve a OUT_OF_HOURS_MENU.
// Si viene del menú principal, vuelve a MAIN_MENU.
func showHelpHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		msg1 := "📋 *GUIA RAPIDA DEL BOT*\n\n" +
			"Soy tu asistente virtual para gestionar citas medicas por WhatsApp. " +
			"Esto es lo que puedo hacer:\n\n" +
			"*1. Agendar cita*\n" +
			"Seleccionas tu tipo de entidad (EPS, particular, etc.), " +
			"ingresas tu documento, y envias una foto de tu orden medica. " +
			"Yo la leo automaticamente y te muestro los horarios disponibles.\n\n" +
			"*2. Consultar citas*\n" +
			"Ingresas tu documento y te muestro tus citas programadas. " +
			"Puedes confirmarlas o cancelarlas.\n\n" +
			"*3. Consultar resultados*\n" +
			"Te comparto el enlace para descargar tus resultados medicos.\n\n" +
			"*4. Ubicacion*\n" +
			"Te muestro las direcciones de nuestras sedes con enlace a Google Maps."

		msg2 := "💡 *TIPS PARA USAR EL BOT*\n\n" +
			"• *Documento:* Ingresa solo numeros, sin puntos ni espacios\n" +
			"• *Orden medica:* Envia una foto clara donde se lean bien los procedimientos\n" +
			"• *Seleccionar opciones:* Cuando te muestre una lista, toca el boton para ver las opciones\n" +
			"• *Horarios:* Te mostrare los horarios disponibles mas cercanos. Si no hay, puedes quedar en lista de espera\n" +
			"• *Volver al menu:* Escribe *menu* o *0* en cualquier momento para volver al inicio\n\n" +
			"Si en algun momento necesitas ayuda humana, el bot te conectara con un agente.\n\n" +
			"*Horario de atencion:*\n" +
			"Lunes a viernes: 7:00 AM - 6:00 PM\n" +
			"Sabados: 7:00 AM - 12:00 PM"

		// Si viene del menú fuera de horario, volver allá
		if sess.GetContext("help_source") == "ooh" {
			r := sm.NewResult(sm.StateOutOfHoursMenu).
				WithText(msg1).
				WithClearCtx("help_source").
				WithEvent("help_shown", nil)
			r.Messages = append(r.Messages, &sm.ListMessage{
				Body:  msg2 + "\n\nEn que puedo ayudarte hoy?",
				Title: "Ver opciones",
				Sections: []sm.ListSection{{
					Title: "Opciones disponibles",
					Rows: []sm.ListRow{
						{ID: "ooh_resultados", Title: "Consultar Resultados", Description: "Si quieres descargar resultados de tus consultas"},
						{ID: "ooh_ubicacion", Title: "Ubicacion", Description: "Conoce nuestras sedes"},
						{ID: "ooh_ayuda", Title: "Como usar el bot", Description: "Guia rapida de como interactuar conmigo"},
					},
				}},
			})
			return r, nil
		}

		r := sm.NewResult(sm.StateMainMenu).
			WithText(msg1).
			WithEvent("help_shown", nil)
		list := buildMainMenuList()
		list.Body = msg2 + "\n\n" + list.Body
		r.Messages = append(r.Messages, list)
		return r, nil
	}
}
