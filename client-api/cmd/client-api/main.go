package main

import (
	"bluegrass/client-api/internal/auth"
	"bluegrass/client-api/internal/config"
	"bluegrass/client-api/internal/dbpool"
	"bluegrass/client-api/internal/httpapi"
	"bluegrass/client-api/internal/session"
	"bluegrass/client-api/internal/source"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"
)

var version = "0.1.0-go"

func main() {
	if err := run(); err != nil {
		log.Printf("client-api: %s", err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "", "private JSON configuration path (outside website root)")
	migrate := flag.Bool("migrate", false, "create only this API's own session tables, then exit")
	check := flag.Bool("check", false, "check database pools, source read-only mode and indexes, then exit")
	explainCustomer := flag.Int64("explain-customer", 0, "with -check, show read-only node EXPLAIN for a specified customer ID")
	prune := flag.Bool("prune", false, "remove one bounded batch of old session data, then exit")
	keygen := flag.Bool("generate-key", false, "print a new random token key for initial private configuration")
	showVersion := flag.Bool("version", false, "print build version")
	flag.Parse()
	if *explainCustomer < 0 || (*explainCustomer > 0 && !*check) {
		return errors.New("-explain-customer requires -check and a positive customer ID")
	}
	if *showVersion {
		fmt.Println("BlueGrass Client API", version)
		return nil
	}
	if *keygen {
		key := make([]byte, 32)
		if _, e := rand.Read(key); e != nil {
			return errors.New("key generation failed")
		}
		fmt.Println(base64.StdEncoding.EncodeToString(key))
		return nil
	}
	if *path == "" {
		return errors.New("-config is required")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	key, _ := base64.StdEncoding.DecodeString(cfg.TokenKey)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return errors.New("source timezone unavailable")
	}
	sourceDB, err := dbpool.Open(cfg.Source, true, loc)
	if err != nil {
		return err
	}
	defer sourceDB.Close()
	sessionDB, err := dbpool.Open(cfg.Sessions, false, time.UTC)
	if err != nil {
		return err
	}
	defer sessionDB.Close()
	store, err := session.New(sessionDB, session.Config{Prefix: cfg.SessionPrefix, Key: key, Timeout: time.Duration(cfg.TransactionTimeoutSeconds) * time.Second})
	if err != nil {
		return err
	}
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *migrate {
		if err = store.Migrate(root); err != nil {
			return errors.New("session schema migration failed; business tables were not migrated")
		}
		fmt.Println("Independent session schema ready")
		return nil
	}
	if *prune {
		if err = store.Prune(root, time.Now().UTC()); err != nil {
			return errors.New("session cleanup failed")
		}
		fmt.Println("One bounded session cleanup batch complete")
		return nil
	}
	if err = checkDatabases(root, sourceDB, sessionDB, cfg); err != nil {
		return err
	}
	repo, err := source.New(sourceDB, source.Config{QueryTimeout: time.Duration(cfg.QueryTimeoutSeconds) * time.Second, SourceLocation: loc, PhoneRefreshInterval: time.Duration(cfg.PhoneRefreshSeconds) * time.Second, MaxPhoneEntries: cfg.MaxPhoneEntries})
	if err != nil {
		return err
	}
	if *check {
		if *explainCustomer > 0 {
			plan, e := repo.ExplainNodes(root, *explainCustomer, 0, 100)
			if e != nil {
				return errors.New("node query plan unavailable")
			}
			fmt.Println(plan)
		}
		return nil
	}
	warm, cancel := context.WithTimeout(root, 30*time.Second)
	err = repo.RefreshPhones(warm)
	cancel()
	if err != nil {
		return errors.New("phone directory warmup failed; service not started")
	}
	go repo.RunPhoneRefresh(root)
	service, err := auth.New(repo, store, auth.Config{Key: key, AccessTTL: time.Duration(cfg.AccessTTLSeconds) * time.Second, RefreshIdle: time.Duration(cfg.RefreshIdleDays) * 24 * time.Hour, RetryWindow: time.Duration(cfg.RetrySeconds) * time.Second, HashWait: time.Duration(cfg.HashWaitSeconds) * time.Second, HashWorkers: cfg.HashWorkers})
	if err != nil {
		return err
	}
	handler, err := httpapi.New(service, repo, httpapi.Config{MaxInflight: cfg.MaxInflight, RequestTimeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second, RequireHTTPS: cfg.RequireHTTPS, TrustedProxyCIDRs: cfg.TrustedProxyCIDRs, LoginPerMinute: cfg.LoginPerMinute, RefreshPerMinute: cfg.RefreshPerMinute, ReadPerMinute: cfg.ReadPerMinute, MaxRateKeys: 10000})
	if err != nil {
		return err
	}
	go poolStats(root, sourceDB, sessionDB)
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: time.Duration(cfg.RequestTimeoutSeconds+5) * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-root.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := server.Shutdown(ctx); e != nil {
			_ = server.Close()
		}
	}()
	log.Printf("BlueGrass API %s listening on %s; pool budgets source=%d sessions=%d", version, cfg.Listen, cfg.Source.MaxOpen, cfg.Sessions.MaxOpen)
	err = server.ListenAndServe()
	stop()
	<-stopped
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("HTTP listener failed")
	}
	return nil
}

func poolStats(ctx context.Context, sourceDB, sessionDB *sql.DB) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			source, session := sourceDB.Stats(), sessionDB.Stats()
			log.Printf("db-pools source_open=%d source_in_use=%d source_waits=%d session_open=%d session_in_use=%d session_waits=%d", source.OpenConnections, source.InUse, source.WaitCount, session.OpenConnections, session.InUse, session.WaitCount)
		}
	}
}

func checkDatabases(ctx context.Context, sourceDB, sessionDB *sql.DB, cfg config.Config) error {
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sourceDB.PingContext(call); err != nil {
		return errors.New("source database unavailable")
	}
	if err := sessionDB.PingContext(call); err != nil {
		return errors.New("session database unavailable")
	}
	var readOnly int
	var isolation string
	if err := sourceDB.QueryRowContext(call, "SELECT @@session.transaction_read_only,@@session.transaction_isolation").Scan(&readOnly, &isolation); err != nil || readOnly != 1 {
		return errors.New("source connection is not read-only")
	}
	rows, err := sessionDB.QueryContext(call, "SELECT id FROM `"+cfg.SessionPrefix+"sessions` LIMIT 0")
	if err != nil {
		return errors.New("session schema missing; run the explicit -migrate command first")
	}
	rows.Close()
	var usernameIndex, phoneIndex int
	query := "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='customers' AND column_name=? AND seq_in_index=1"
	if err = sourceDB.QueryRowContext(call, query, "username").Scan(&usernameIndex); err != nil || usernameIndex == 0 {
		return errors.New("customers.username must have a leading index before serving logins")
	}
	if err = sourceDB.QueryRowContext(call, query, "phone").Scan(&phoneIndex); err != nil {
		return errors.New("cannot inspect source lookup indexes")
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"source_read_only": readOnly == 1, "source_isolation": isolation, "username_indexed": true, "phone_indexed": phoneIndex > 0, "source_pool_max": cfg.Source.MaxOpen, "session_pool_max": cfg.Sessions.MaxOpen})
	return nil
}
