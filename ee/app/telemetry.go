package app

import (
	"fmt"
	"os"

	"github.com/zybuu-ai/abhed/ee/telemetry"
	"github.com/zybuu-ai/abhed/app"
	"github.com/zybuu-ai/abhed/config"
	abhed "github.com/zybuu-ai/abhed/sdk"
)

// Telemetry exports the event stream as OpenTelemetry traces, when an
// operator has somewhere to send them.
func Telemetry() app.Option {
	return func(a *app.App) {
		app.WithFeature("telemetry")(a)
		app.WithEventTap(buildTelemetry)(a)
	}
}

func buildTelemetry(cfg config.Config) (func(abhed.Event), func(), error) {
	if !cfg.Telemetry.Enabled || cfg.Telemetry.Endpoint == "" {
		return nil, nil, nil
	}
	exp := telemetry.New(telemetry.Config{
		Endpoint:    cfg.Telemetry.Endpoint,
		Headers:     cfg.Telemetry.Headers,
		ServiceName: cfg.Telemetry.ServiceName,
	})
	fmt.Fprintf(os.Stderr, "  telemetry %s\n", cfg.Telemetry.Endpoint)
	return exp.Observe, exp.Close, nil
}
