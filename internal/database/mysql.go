package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/neuro-bot/neuro-bot/internal/config"
)

func NewLocalDB(cfg *config.Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=America%%2FBogota&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBDatabase)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("local db open: %w", err)
	}

	db.SetMaxOpenConns(cfg.LocalDBMaxOpen)
	db.SetMaxIdleConns(cfg.LocalDBMaxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("local db ping: %w", err)
	}

	return db, nil
}

func NewExternalDB(cfg *config.Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=America%%2FBogota&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		cfg.ExtDBUser, cfg.ExtDBPassword, cfg.ExtDBHost, cfg.ExtDBPort, cfg.ExtDBDatabase)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("external db open: %w", err)
	}

	db.SetMaxOpenConns(cfg.ExternalDBMaxOpen)
	db.SetMaxIdleConns(cfg.ExternalDBMaxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("external db ping: %w", err)
	}

	return db, nil
}
