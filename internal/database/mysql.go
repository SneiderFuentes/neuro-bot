package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/neuro-bot/neuro-bot/internal/config"
)

func NewLocalDB(cfg *config.Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=America%%2FBogota&charset=utf8mb4",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBDatabase)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("local db open: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("local db ping: %w", err)
	}

	return db, nil
}

func NewExternalDB(cfg *config.Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=America%%2FBogota&charset=utf8mb4",
		cfg.ExtDBUser, cfg.ExtDBPassword, cfg.ExtDBHost, cfg.ExtDBPort, cfg.ExtDBDatabase)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("external db open: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("external db ping: %w", err)
	}

	return db, nil
}
