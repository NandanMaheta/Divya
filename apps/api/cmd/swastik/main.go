// Command swastik runs the Swastik API ("serve") or applies migrations ("migrate up|down|status").
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/health"
	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/config"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/tickauth"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/migrations"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log, os.Args[1:]); err != nil {
		log.Error("fatal", slog.Any("err", err))
		os.Exit(1)
	}
}

func run(log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: swastik serve | swastik migrate up|down|status | swastik openapi")
	}
	if args[0] == "openapi" {
		// Prints the OpenAPI 3.1 spec without needing a database; used to generate the TS client.
		// The tick is registered (hidden) with a ticker that is never called, so the command
		// proves the public document stays free of it.
		spec, err := buildAPI(deps{Tick: (*scheduler.Service)(nil), TickAuth: tickauth.Disabled()}).OpenAPI().YAML()
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(spec)
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch args[0] {
	case "migrate":
		direction := "up"
		if len(args) > 1 {
			direction = args[1]
		}
		return db.Migrate(ctx, pool, migrations.FS, direction)
	case "serve":
		return serve(ctx, log, cfg, pool)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// deps are the collaborators the API's operations need. The zero value registers only the
// operations that need none.
type deps struct {
	Pool     health.Pinger
	Tick     scheduler.Ticker
	TickAuth tickauth.Authenticator
}

// buildAPI registers every module's operations. New modules are wired here.
func buildAPI(d deps) huma.API {
	_, api := httpx.NewAPI()
	health.Register(api, health.NewService(d.Pool, httpx.Version, time.Now))
	if d.Tick != nil {
		scheduler.RegisterTick(api, d.Tick, d.TickAuth)
	}
	return api
}

// selectTickAuth picks the tick authenticator from the configuration. Production verifies a
// Google-signed ID token; without audience and service account the tick is closed (every call
// is rejected) and a warning says so.
func selectTickAuth(ctx context.Context, log *slog.Logger, cfg config.Config) tickauth.Authenticator {
	t := cfg.Tick
	if t.Auth == "dev" {
		if t.DevToken == "" {
			log.Warn("tick auth is dev but TICK_DEV_TOKEN is empty: the tick endpoint rejects every call")
		}
		return tickauth.NewDev(t.DevToken)
	}
	if t.Audience == "" || t.ServiceAccount == "" {
		log.Warn("TICK_AUDIENCE or TICK_SERVICE_ACCOUNT is not set: the tick endpoint rejects every call")
		return tickauth.Disabled()
	}
	return tickauth.NewOIDC(ctx, t.Audience, t.ServiceAccount, clock.Real)
}

func serve(ctx context.Context, log *slog.Logger, cfg config.Config, pool *pgxpool.Pool) error {
	txr := db.NewTxRunner(pool)
	ob := outbox.New(txr, clock.Real, log, outbox.DefaultOptions())
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	sched := scheduler.New(txr, ob, clock.Real, log, host, scheduler.DefaultOptions())
	sched.Register(scheduler.RetentionKind, sched.RetentionHandler(), scheduler.RetentionOptions())

	api := buildAPI(deps{Pool: pool, Tick: sched, TickAuth: selectTickAuth(ctx, log, cfg)})
	srv := httpx.NewServer(cfg.HTTPAddr, api.Adapter(), log)
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", slog.String("addr", cfg.HTTPAddr), slog.String("env", cfg.Env))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// The parent context is already cancelled, so derive a fresh, bounded one for draining.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
