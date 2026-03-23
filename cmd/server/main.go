package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/api"
	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/config"
	"github.com/neuro-bot/neuro-bot/internal/database"
	"github.com/neuro-bot/neuro-bot/internal/notifications"
	"github.com/neuro-bot/neuro-bot/internal/repository"
	"github.com/neuro-bot/neuro-bot/internal/repository/datosipsndx"
	localrepo "github.com/neuro-bot/neuro-bot/internal/repository/local"
	"github.com/neuro-bot/neuro-bot/internal/scheduler"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	"github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/statemachine/handlers"
	tg "github.com/neuro-bot/neuro-bot/internal/telegram"
	"github.com/neuro-bot/neuro-bot/internal/tracking"
	"github.com/neuro-bot/neuro-bot/internal/worker"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := config.Load()

	// Configurar logger
	initLogger(cfg.LogLevel)

	// Telegram error alerts (optional — wraps slog handler)
	if tgClient := tg.NewClient(cfg.TelegramBotToken, cfg.TelegramChatID); tgClient != nil {
		alertHandler := tg.NewAlertHandler(slog.Default().Handler(), tgClient)
		go alertHandler.Start(ctx)
		slog.SetDefault(slog.New(alertHandler))
		slog.Info("telegram error alerts enabled")
	}

	// Configurar timezone
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("invalid timezone %s: %v", cfg.Timezone, err)
	}
	time.Local = loc

	// Conectar BD local
	localDB, err := database.NewLocalDB(cfg)
	if err != nil {
		log.Fatalf("local db: %v", err)
	}
	defer localDB.Close()

	// Conectar BD externa (no fatal si falla — health check mostrará "degraded")
	externalDB, err := database.NewExternalDB(cfg)
	if err != nil {
		slog.Warn("external db not available, bot will start in degraded mode", "error", err)
	} else {
		defer externalDB.Close()
	}

	// Migraciones (BD local)
	if err := database.RunMigrations(localDB, "migrations"); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	// Repositorios — selección por EXTERNAL_DB_DRIVER (R-ARQ-01)
	var repos *repository.Repositories
	if externalDB != nil {
		repos = initRepositories(cfg.ExternalDBDriver, externalDB)
	}

	// Session manager (BD local + phone mutex)
	sessionRepo := localrepo.NewSessionRepo(localDB)
	sessionManager := session.NewSessionManager(sessionRepo, cfg.SessionTimeoutMinutes)

	// Iniciar phone mutex cleanup
	go sessionManager.PhoneMutex().StartCleanup(ctx)

	// Bird client
	birdClient := bird.NewClient(cfg)

	// Services (dependen de repos)
	var patientSvc *services.PatientService
	var appointmentSvc *services.AppointmentService

	// Build CUPS context for OCR prompt (reference table for AI matching)
	cupsContext := ""
	if repos != nil {
		if allProcs, err := repos.Procedure.FindAllActive(ctx); err == nil {
			entries := make([]struct{ Code, Name string }, len(allProcs))
			for i, p := range allProcs {
				entries[i] = struct{ Code, Name string }{p.Code, p.Name}
			}
			cupsContext = services.BuildCupsContext(entries)
			slog.Info("cups context loaded", "procedures", len(allProcs))
		} else {
			slog.Warn("failed to load cups context for OCR", "error", err)
		}
	}
	ocrSvc := services.NewOCRService(cfg.OpenAIAPIKey, cfg.OpenAIModel, cupsContext, cfg.BirdAccessKeyID)

	if repos != nil {
		patientSvc = services.NewPatientService(repos.Patient)
		appointmentSvc = services.NewAppointmentService(repos.Appointment, cfg)
	}

	// State machine (interceptores + handlers se registran por fase)
	machine := statemachine.NewMachine()
	statemachine.SetMaxRetries(cfg.MaxRetries)
	statemachine.RegisterInterceptors(machine)

	// Location repo (local DB)
	locationRepo := localrepo.NewLocationRepo(localDB)

	// Address mapper (maps procedure addresses → Google Maps URLs from center_locations)
	var addrMapper *services.AddressMapper
	if locations, err := locationRepo.FindActive(ctx); err == nil && len(locations) > 0 {
		addrMapper = services.NewAddressMapper(locations)
		slog.Info("address mapper loaded", "locations", len(locations))
	}

	// Fase 5: Saludo e Identificación + Results/Locations
	handlers.RegisterGreetingHandlers(machine, cfg, locationRepo)
	handlers.RegisterResultsAndLocationHandlers(machine, cfg, locationRepo)
	if patientSvc != nil {
		handlers.RegisterIdentificationHandlers(machine, patientSvc)
	}
	// Fase 5.5: Entity Management (existing patients)
	if repos != nil {
		handlers.RegisterEntityManagementHandlers(machine, repos.Entity, repos.Patient)
	}
	// Fase 6: Registro de Pacientes
	if repos != nil && patientSvc != nil {
		handlers.RegisterRegistrationHandlers(machine, patientSvc, repos.Municipality)
	}
	// Fase 7: Consulta y Gestión de Citas
	// Cambio 13: CancellationCallback — notifyManager captured by reference (assigned later, before server accepts requests)
	var notifyManager *notifications.NotificationManager
	onCancel := handlers.CancellationCallback(func(ctx context.Context, cupsCode string) {
		if notifyManager != nil {
			notifyManager.CheckWaitingListForCups(ctx, cupsCode)
		}
	})
	if appointmentSvc != nil {
		var procRepoForAppts repository.ProcedureRepository
		if repos != nil {
			procRepoForAppts = repos.Procedure
		}
		handlers.RegisterAppointmentHandlers(machine, appointmentSvc, procRepoForAppts, addrMapper, onCancel)
	}
	// Fase 8: Orden Médica y OCR
	if repos != nil {
		handlers.RegisterMedicalOrderHandlers(machine, ocrSvc, repos.Procedure)
	}
	// Fase 9: Validaciones Médicas
	gfrSvc := services.NewGFRService()
	if appointmentSvc != nil {
		handlers.RegisterMedicalValidationHandlers(machine, gfrSvc, appointmentSvc)
	}
	// Fase 10 + 13: Búsqueda de Slots y Agendamiento + Lista de Espera
	waitingListRepo := localrepo.NewWaitingListRepo(localDB)
	var slotSvc *services.SlotService
	if repos != nil && appointmentSvc != nil {
		slotSvc = services.NewSlotService(repos.Doctor, repos.Schedule)
		handlers.RegisterSlotHandlers(machine, slotSvc, appointmentSvc, repos.Procedure, repos.Soat, repos.Entity, waitingListRepo, addrMapper)
	}
	// Fase 11: Post-Acción y Escalación
	handlers.RegisterPostActionHandlers(machine)
	handlers.RegisterEscalationHandlers(machine, birdClient, cfg)

	// Fase 12: Notificaciones Proactivas y Scheduler
	if appointmentSvc != nil {
		notifyManager = notifications.NewNotificationManager(birdClient, appointmentSvc, cfg)
	}

	// Fase 14: Event Tracking
	eventRepo := localrepo.NewEventRepo(localDB)
	tracker := tracking.NewEventTracker(eventRepo)

	// Fase 22: Notification persistence + preparations + tracking
	notifRepo := localrepo.NewNotificationRepo(localDB)
	if notifyManager != nil {
		notifyManager.SetPersister(notifRepo)
		notifyManager.SetTracker(tracker)
		if repos != nil {
			notifyManager.SetProcedureRepo(repos.Procedure)
		}
		if addrMapper != nil {
			notifyManager.SetAddressMapper(addrMapper)
		}
		notifyManager.RestorePending(ctx)
		go notifyManager.StartExpirationChecker(ctx)
	}

	// Fase 20: Inactivity checker (reminders + auto-close for active, expire for escalated)
	go sessionManager.StartInactivityChecker(ctx, session.InactivityDeps{
		BirdClient:   birdClient,
		Tracker:      tracker,
		Reminder1Min: cfg.InactivityReminder1Min,
		Reminder2Min: cfg.InactivityReminder2Min,
		CloseMin:     cfg.InactivityCloseMin,
	})

	// Message inbox (WAL for crash recovery)
	inboxRepo := localrepo.NewInboxRepo(localDB)

	// Worker pool (10 workers, buffer 100, overflow hasta 20)
	workerPool := worker.NewMessageWorkerPool(10, 100)
	workerPool.SetDependencies(sessionManager, birdClient, machine)
	workerPool.SetTracker(tracker)
	workerPool.SetOCRService(ocrSvc)
	workerPool.SetInboxRepo(inboxRepo)
	workerPool.Start(ctx)

	// WAL replay: re-process messages that weren't completed before last shutdown/crash
	if pending, err := inboxRepo.FindPending(ctx); err != nil {
		slog.Error("inbox replay query failed", "error", err)
	} else if len(pending) > 0 {
		slog.Info("replaying unprocessed inbox messages", "count", len(pending))
		for _, row := range pending {
			var event bird.WebhookEvent
			if err := json.Unmarshal([]byte(row.RawBody), &event); err != nil {
				slog.Error("inbox replay parse failed", "id", row.ID, "error", err)
				inboxRepo.MarkDone(ctx, row.ID)
				continue
			}
			msg := bird.ParseInboundMessage(event)
			workerPool.Enqueue(msg)
		}
	}

	// Fase 13: Inyectar dependencias de lista de espera al NotificationManager
	if notifyManager != nil {
		notifyManager.SetWaitingListDeps(waitingListRepo, sessionRepo, workerPool)
	}

	// Cambio 13: Inyectar dependencias para WL check en tiempo real
	if notifyManager != nil && slotSvc != nil && repos != nil {
		notifyManager.SetWaitingListCheckDeps(slotSvc, repos.Appointment, waitingListRepo)
	}

	var schedulerTasks *scheduler.Tasks
	if repos != nil && notifyManager != nil {
		schedulerRunRepo := localrepo.NewSchedulerRunRepo(localDB)

		sched := scheduler.NewScheduler(loc)
		sched.SetRunRepo(schedulerRunRepo)
		schedulerTasks = &scheduler.Tasks{
			AppointmentRepo: repos.Appointment,
			AppointmentSvc:  appointmentSvc,
			BirdClient:      birdClient,
			NotifyManager:   notifyManager,
			WaitingListRepo: waitingListRepo,
			SlotService:     slotSvc,
			Cfg:             cfg,
			Tracker:         tracker,
			InboxRepo:       inboxRepo,
		}
		schedulerTasks.RegisterAll(sched)
		sched.RunMissedTasks(ctx) // Catch-up missed tasks before starting the regular loop
		go sched.Start(ctx)
	}

	// Webhook handler (con NotificationManager para postbacks proactivos + WAL inbox)
	webhookHandler := api.NewWebhookHandler(birdClient, workerPool, notifyManager, cfg)
	webhookHandler.SetInboxRepo(inboxRepo)

	// Fase 13+14: Internal API endpoints (protegidos con API key)
	startTime := time.Now()
	var internalHandler *api.InternalHandler
	if repos != nil && notifyManager != nil {
		internalHandler = api.NewInternalHandler(
			repos.Appointment, repos.Schedule, waitingListRepo, eventRepo,
			birdClient, notifyManager, notifyManager, workerPool,
			tracker, cfg, startTime,
		)
		if schedulerTasks != nil {
			internalHandler.SetReminderRunner(schedulerTasks)
		}
	}

	// HTTP Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler(localDB, externalDB))
	mux.HandleFunc("POST /api/webhooks/whatsapp", webhookHandler.HandleWhatsApp)
	mux.HandleFunc("POST /api/webhooks/whatsapp/outbound", webhookHandler.HandleWhatsAppOutbound)
	mux.HandleFunc("POST /api/webhooks/conversations", webhookHandler.HandleConversation)

	if internalHandler != nil {
		internalMux := http.NewServeMux()
		internalMux.HandleFunc("POST /api/internal/cancel-agenda", internalHandler.HandleCancelAgenda)
		internalMux.HandleFunc("POST /api/internal/reschedule-agenda", internalHandler.HandleRescheduleAgenda)
		internalMux.HandleFunc("POST /api/internal/waiting-list/check", internalHandler.HandleWaitingListCheck)
		internalMux.HandleFunc("GET /api/internal/waiting-list", internalHandler.HandleWaitingListGet)
		// Fase 14: KPI endpoints
		internalMux.HandleFunc("GET /api/internal/kpis/daily", internalHandler.HandleDailyKPIs)
		internalMux.HandleFunc("GET /api/internal/kpis/weekly", internalHandler.HandleWeeklyKPIs)
		internalMux.HandleFunc("GET /api/internal/kpis/funnel", internalHandler.HandleFunnel)
		internalMux.HandleFunc("GET /api/internal/kpis/health", internalHandler.HandleHealthKPIs)
		internalMux.HandleFunc("POST /api/internal/test-alert", internalHandler.HandleTestAlert)
		internalMux.HandleFunc("POST /api/internal/send-reminders", internalHandler.HandleSendReminders)
		mux.Handle("/api/internal/",
			api.RateLimiter(30, time.Minute)(
				api.MaxBodySize(
					api.InternalAuth(cfg.InternalAPIKey)(internalMux),
				),
			),
		)
	}

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: api.RequestLogger(mux)}
	go func() {
		slog.Info("server starting", "port", cfg.Port, "timezone", cfg.Timezone)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down...")

	// 1. Stop accepting new HTTP requests
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	// 2. Wait for worker pool goroutines to finish (before DB close via defers)
	workerPool.Stop()

	slog.Info("shutdown complete")
}

func initLogger(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
}

func initRepositories(driver string, externalDB *sql.DB) *repository.Repositories {
	switch driver {
	case "datosipsndx":
		return &repository.Repositories{
			Patient:      datosipsndx.NewPatientRepo(externalDB),
			Appointment:  datosipsndx.NewAppointmentRepo(externalDB),
			Doctor:       datosipsndx.NewDoctorRepo(externalDB),
			Schedule:     datosipsndx.NewScheduleRepo(externalDB),
			Procedure:    datosipsndx.NewProcedureRepo(externalDB),
			Entity:       datosipsndx.NewEntityRepo(externalDB),
			Municipality: datosipsndx.NewMunicipalityRepo(externalDB),
			Soat:         datosipsndx.NewSoatRepo(externalDB),
		}
	default:
		log.Fatalf("unknown EXTERNAL_DB_DRIVER: %s", driver)
		return nil
	}
}

func healthHandler(localDB, externalDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := map[string]string{"status": "ok"}

		if err := localDB.Ping(); err != nil {
			health["local_db"] = "error: " + err.Error()
			health["status"] = "degraded"
		} else {
			health["local_db"] = "ok"
		}

		if externalDB == nil {
			health["external_db"] = "not connected"
			health["status"] = "degraded"
		} else if err := externalDB.Ping(); err != nil {
			health["external_db"] = "error: " + err.Error()
			health["status"] = "degraded"
		} else {
			health["external_db"] = "ok"
		}

		w.Header().Set("Content-Type", "application/json")
		if health["status"] != "ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(health)
	}
}
