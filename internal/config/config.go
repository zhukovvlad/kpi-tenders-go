package config

import (
	"fmt"
	"net/url"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

const minSecretLen = 32

type Config struct {
	AppEnv  string `env:"APP_ENV"  env-default:"local"`
	AppPort string `env:"APP_PORT" env-default:"8080"`

	DBURL string `env:"DB_URL" env-required:"true"`

	RedisURL string `env:"REDIS_URL" env-default:"redis://localhost:6379/0"`

	// Watchdog: how often to scan for stuck tasks.
	WatchdogInterval time.Duration `env:"WATCHDOG_INTERVAL" env-default:"2m"`
	// Watchdog: a task is considered stale if it has been in 'processing' longer than this.
	WatchdogThreshold time.Duration `env:"WATCHDOG_THRESHOLD" env-default:"10m"`
	// Watchdog: max number of re-queue attempts before permanently failing the task.
	WatchdogMaxRetries int `env:"WATCHDOG_MAX_RETRIES" env-default:"5"`

	JWTAccessSecret  string `env:"JWT_ACCESS_SECRET"  env-required:"true"`
	JWTRefreshSecret string `env:"JWT_REFRESH_SECRET" env-required:"true"`
	ServiceToken     string `env:"SERVICE_TOKEN"      env-required:"true"`

	S3Endpoint  string `env:"S3_ENDPOINT"   env-default:"localhost:9000"`
	S3Region    string `env:"S3_REGION"     env-default:"us-east-1"`
	S3AccessKey string `env:"S3_ACCESS_KEY"`
	S3SecretKey string `env:"S3_SECRET_KEY"`
	S3Bucket    string `env:"S3_BUCKET"     env-default:"tenders"`
	S3UseSSL    bool   `env:"S3_USE_SSL"    env-default:"false"`
}

// MustLoad reads configuration from environment variables.
// It tries to read an optional .env file first.
func MustLoad() *Config {
	var cfg Config

	// Attempt to read .env; ignore error if file is missing.
	_ = cleanenv.ReadConfig(".env", &cfg)

	if err := cleanenv.ReadEnv(&cfg); err != nil {
		panic(fmt.Sprintf("config: %s", err))
	}

	if err := cfg.validate(); err != nil {
		panic(fmt.Sprintf("config: %s", err))
	}

	return &cfg
}

// validate checks that security-sensitive fields meet minimum requirements.
func (c *Config) validate() error {
	if len(c.JWTAccessSecret) < minSecretLen {
		return fmt.Errorf("JWT_ACCESS_SECRET must be at least %d characters", minSecretLen)
	}
	if len(c.JWTRefreshSecret) < minSecretLen {
		return fmt.Errorf("JWT_REFRESH_SECRET must be at least %d characters", minSecretLen)
	}
	if len(c.ServiceToken) < minSecretLen {
		return fmt.Errorf("SERVICE_TOKEN must be at least %d characters", minSecretLen)
	}
	redisURL, err := url.Parse(c.RedisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL is not a valid URL: %w", err)
	}
	if (redisURL.Scheme != "redis" && redisURL.Scheme != "rediss") || redisURL.Host == "" {
		return fmt.Errorf("REDIS_URL must be a valid redis:// or rediss:// URL (got scheme=%q host=%q)", redisURL.Scheme, redisURL.Host)
	}
	return nil
}
