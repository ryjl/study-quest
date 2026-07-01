package config

import (
	"os"
)

// Config holds the simple bootstrap configuration for the server.
type Config struct {
	ServerAddr string
	DBPath     string
}

// LoadConfig reads configuration from environment variables with safe defaults.
func LoadConfig() *Config {
	serverAddr := os.Getenv("SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = "0.0.0.0:8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/studyquest.db"
	}

	return &Config{
		ServerAddr: serverAddr,
		DBPath:     dbPath,
	}
}
