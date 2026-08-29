package handler

import "os"

func lookupEnv(k string) string { return os.Getenv(k) }

func lookupEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
