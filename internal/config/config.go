package config

import (
	"fmt"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	AppEnv  string `env:"APP_ENV"  env-default:"local"`
	AppPort string `env:"APP_PORT" env-default:"8080"`

	DBURL string `env:"DB_URL" env-required:"true"`

	RedisURL string `env:"REDIS_URL" env-default:"redis://localhost:6379/0"`

	JWTAccessSecret  string `env:"JWT_ACCESS_SECRET"  env-required:"true"`
	JWTRefreshSecret string `env:"JWT_REFRESH_SECRET" env-required:"true"`
	ServiceToken     string `env:"SERVICE_TOKEN"      env-required:"true"`

	S3Endpoint  string `env:"S3_ENDPOINT"   env-default:"localhost:9000"`
	S3Region    string `env:"S3_REGION"     env-default:"us-east-1"`
	S3AccessKey string `env:"S3_ACCESS_KEY" env-required:"true"`
	S3SecretKey string `env:"S3_SECRET_KEY" env-required:"true"`
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

	return &cfg
}
