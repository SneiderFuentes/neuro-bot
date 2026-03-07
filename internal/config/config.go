package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	// App
	Port     string
	Timezone string
	LogLevel string

	// Local DB
	DBHost     string
	DBPort     string
	DBDatabase string
	DBUser     string
	DBPassword string

	// External DB
	ExtDBHost     string
	ExtDBPort     string
	ExtDBDatabase string
	ExtDBUser     string
	ExtDBPassword string

	// External DB Driver — R-ARQ-01
	ExternalDBDriver string // "datosipsndx" | "nuevo_software"

	// Bird
	BirdAPIURL        string
	BirdAPIKeyWA      string
	BirdAccessKeyID   string
	BirdWebhookSecret         string
	BirdWebhookSecretOutbound string // Separate signing key for outbound webhook (optional)
	BirdWorkspaceID   string
	BirdChannelID     string
	BirdTeamGrupoA    string // Ecografías, RX, Resonancia, TAC
	BirdTeamGrupoB    string // Neurología, Fisiatría, Estudios del sueño
	BirdTeamFallback  string // Call Center (genérico)
	BirdAgentFallback string // Líder Call Center — fallback si equipo no disponible

	// Bird Templates
	BirdTemplateConfirmProjectID     string
	BirdTemplateConfirmVersionID     string
	BirdTemplateConfirmLocale        string
	BirdTemplateRescheduleProjectID  string
	BirdTemplateRescheduleVersionID  string
	BirdTemplateRescheduleLocale     string
	BirdTemplateWaitingListProjectID  string
	BirdTemplateWaitingListVersionID  string
	BirdTemplateWaitingListLocale     string
	BirdTemplateCancellationProjectID string
	BirdTemplateCancellationVersionID string
	BirdTemplateCancellationLocale    string

	// Bird Channel for Templates
	BirdChannelIDTemplates string

	// Bird Voice
	BirdAPIKeyVoice string
	BirdVoiceFlowID string

	// OpenAI
	OpenAIAPIKey string
	OpenAIModel  string

	// Session
	SessionTimeoutMinutes int

	// Inactivity reminders (minutes without patient response)
	InactivityReminder1Min int // First reminder
	InactivityReminder2Min int // Second reminder
	InactivityCloseMin     int // Auto-close session

	// Center
	CenterKey  string
	CenterName string
	BotName    string
	ResultsURL string

	// Security
	InternalAPIKey string

	// Ngrok
	NgrokHostname string

	// Testing
	TestingAlwaysOpen bool // Bypasses business hours check when true
	MaxRetries        int  // Max invalid response attempts before fallback menu
}

func Load() *Config {
	godotenv.Load() // no falla si no existe .env

	cfg := &Config{
		// App
		Port:     getEnv("PORT", "8080"),
		Timezone: getEnv("TZ", "America/Bogota"),
		LogLevel: getEnv("LOG_LEVEL", "info"),

		// Local DB
		DBHost:     getEnv("DB_HOST", "db"),
		DBPort:     getEnv("DB_PORT", "3306"),
		DBDatabase: getEnv("DB_DATABASE", "neuro_bot"),
		DBUser:     getEnv("DB_USER", "botuser"),
		DBPassword: os.Getenv("DB_PASSWORD"),

		// External DB
		ExtDBHost:     getEnv("EXTERNAL_DB_HOST", "host.docker.internal"),
		ExtDBPort:     getEnv("EXTERNAL_DB_PORT", "3306"),
		ExtDBDatabase: getEnv("EXTERNAL_DB_DATABASE", "datosipsndx"),
		ExtDBUser:     os.Getenv("EXTERNAL_DB_USER"),
		ExtDBPassword: os.Getenv("EXTERNAL_DB_PASSWORD"),

		// External DB Driver
		ExternalDBDriver: getEnv("EXTERNAL_DB_DRIVER", "datosipsndx"),

		// Bird
		BirdAPIURL:        os.Getenv("BIRD_API_URL"),
		BirdAPIKeyWA:      os.Getenv("BIRD_API_KEY_WA"),
		BirdAccessKeyID:   os.Getenv("BIRD_ACCESS_KEY_ID"),
		BirdWebhookSecret:         os.Getenv("BIRD_WEBHOOK_SECRET"),
		BirdWebhookSecretOutbound: os.Getenv("BIRD_WEBHOOK_SECRET_OUTBOUND"),
		BirdWorkspaceID:   os.Getenv("BIRD_WORKSPACE_ID"),
		BirdChannelID:     os.Getenv("BIRD_CHANNEL_ID"),
		BirdTeamGrupoA:    os.Getenv("BIRD_TEAM_GRUPO_A"),
		BirdTeamGrupoB:    os.Getenv("BIRD_TEAM_GRUPO_B"),
		BirdTeamFallback:  os.Getenv("BIRD_TEAM_FALLBACK"),
		BirdAgentFallback: os.Getenv("BIRD_AGENT_FALLBACK"),

		// Bird Templates
		BirdTemplateConfirmProjectID:     os.Getenv("BIRD_TEMPLATE_CONFIRM_PROJECT_ID"),
		BirdTemplateConfirmVersionID:     os.Getenv("BIRD_TEMPLATE_CONFIRM_VERSION_ID"),
		BirdTemplateConfirmLocale:        getEnv("BIRD_TEMPLATE_CONFIRM_LOCALE", "es-MX"),
		BirdTemplateRescheduleProjectID:  os.Getenv("BIRD_TEMPLATE_RESCHEDULE_PROJECT_ID"),
		BirdTemplateRescheduleVersionID:  os.Getenv("BIRD_TEMPLATE_RESCHEDULE_VERSION_ID"),
		BirdTemplateRescheduleLocale:     getEnv("BIRD_TEMPLATE_RESCHEDULE_LOCALE", "es-CO"),
		BirdTemplateWaitingListProjectID:  os.Getenv("BIRD_TEMPLATE_WAITING_LIST_PROJECT_ID"),
		BirdTemplateWaitingListVersionID:  os.Getenv("BIRD_TEMPLATE_WAITING_LIST_VERSION_ID"),
		BirdTemplateWaitingListLocale:     getEnv("BIRD_TEMPLATE_WAITING_LIST_LOCALE", "es-CO"),
		BirdTemplateCancellationProjectID: os.Getenv("BIRD_TEMPLATE_CANCELLATION_PROJECT_ID"),
		BirdTemplateCancellationVersionID: os.Getenv("BIRD_TEMPLATE_CANCELLATION_VERSION_ID"),
		BirdTemplateCancellationLocale:    getEnv("BIRD_TEMPLATE_CANCELLATION_LOCALE", "es-CO"),

		// Bird Channel for Templates
		BirdChannelIDTemplates: os.Getenv("BIRD_CHANNEL_ID_TEMPLATES"),

		// Bird Voice
		BirdAPIKeyVoice: os.Getenv("BIRD_API_KEY_VOICE"),
		BirdVoiceFlowID: os.Getenv("BIRD_VOICE_FLOW_ID"),

		// OpenAI
		OpenAIAPIKey: os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:  getEnv("OPENAI_MODEL", "gpt-4o-mini"),

		// Session
		SessionTimeoutMinutes: getEnvInt("SESSION_TIMEOUT_MINUTES", 120),

		// Inactivity
		InactivityReminder1Min: getEnvInt("INACTIVITY_REMINDER_1_MIN", 5),
		InactivityReminder2Min: getEnvInt("INACTIVITY_REMINDER_2_MIN", 15),
		InactivityCloseMin:     getEnvInt("INACTIVITY_CLOSE_MIN", 30),

		// Center
		CenterKey:  getEnv("CENTER_KEY", "datosipsndx"),
		CenterName: getEnv("CENTER_NAME", "Neuro Electrodiagnóstico del Llano"),
		BotName:    getEnv("BOT_NAME", "Samuel"),
		ResultsURL: getEnv("RESULTS_URL", ""),

		// Security
		InternalAPIKey: os.Getenv("INTERNAL_API_KEY"),

		// Ngrok
		NgrokHostname: os.Getenv("NGROK_HOSTNAME"),

		// Testing
		TestingAlwaysOpen: getEnv("TESTING_ALWAYS_OPEN", "") == "true",
		MaxRetries:        getEnvInt("MAX_RETRIES", 4),
	}

	cfg.validate()
	return cfg
}

func (c *Config) validate() {
	required := map[string]string{
		"DB_HOST":             c.DBHost,
		"DB_PASSWORD":         c.DBPassword,
		"EXTERNAL_DB_USER":    c.ExtDBUser,
		"EXTERNAL_DB_PASSWORD": c.ExtDBPassword,
		"BIRD_API_URL":        c.BirdAPIURL,
		"BIRD_API_KEY_WA":     c.BirdAPIKeyWA,
		"BIRD_WEBHOOK_SECRET": c.BirdWebhookSecret,
		"BIRD_WORKSPACE_ID":   c.BirdWorkspaceID,
		"BIRD_CHANNEL_ID":     c.BirdChannelID,
		"BIRD_TEAM_FALLBACK":  c.BirdTeamFallback,
		"OPENAI_API_KEY":      c.OpenAIAPIKey,
		"INTERNAL_API_KEY":    c.InternalAPIKey,
	}

	var missing []string
	for name, value := range required {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		log.Fatalf("Missing required env vars: %s", strings.Join(missing, ", "))
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ResolveTeamForCups returns the Bird team ID based on the CUPS procedure code.
// Falls back to BirdTeamFallback (Call Center) for unknown codes.
func (c *Config) ResolveTeamForCups(cupsCode string) string {
	if len(cupsCode) < 3 {
		return c.BirdTeamFallback
	}
	p3 := cupsCode[:3]
	switch {
	case p3 == "881" || p3 == "882": // Ecografía
		return c.BirdTeamGrupoA
	case p3 == "883": // Resonancia Magnética
		return c.BirdTeamGrupoA
	case p3 == "871" || p3 == "879": // Tomografía (TAC)
		return c.BirdTeamGrupoA
	case p3 == "870" || (p3 >= "873" && p3 <= "878"): // Rayos X
		return c.BirdTeamGrupoA
	case p3 == "291" || p3 == "930" || p3 == "892": // EMG / Fisiatría
		return c.BirdTeamGrupoB
	case cupsCode == "890274" || cupsCode == "890374" || cupsCode == "053105": // Neurología
		return c.BirdTeamGrupoB
	default:
		return c.BirdTeamFallback
	}
}

// ResolveOutboundWebhookSecret returns the outbound webhook secret, falling back to the main secret.
func (c *Config) ResolveOutboundWebhookSecret() string {
	if c.BirdWebhookSecretOutbound != "" {
		return c.BirdWebhookSecretOutbound
	}
	return c.BirdWebhookSecret
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
