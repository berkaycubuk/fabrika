package observability

import (
	"os"
	"time"

	"github.com/getsentry/sentry-go"
)

const DefaultDSN = "https://128519b05f9c2f238ac379c386efe4cb@o4506500501340160.ingest.us.sentry.io/4511529648455680"

func ResolveDSN() string {
	if os.Getenv("FABRIKA_SENTRY_DISABLE") != "" {
		return ""
	}
	if dsn := os.Getenv("FABRIKA_SENTRY_DSN"); dsn != "" {
		return dsn
	}
	return DefaultDSN
}

func Init(dsn, release, environment string) (func(), error) {
	if dsn == "" {
		return func() {}, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:         dsn,
		Release:     release,
		Environment: environment,
	})
	if err != nil {
		return func() {}, err
	}
	return func() { sentry.Flush(2 * time.Second) }, nil
}

func CaptureError(err error) {
	if err == nil {
		return
	}
	sentry.CaptureException(err)
}
