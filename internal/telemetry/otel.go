package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// InitTracerProvider builds the process-wide TracerProvider and installs it
// as the OpenTelemetry global. Phase 0 wires the SDK and resource attributes
// only — no exporter is pinned in go.mod yet, so spans are created and
// sampled but not shipped anywhere until a later phase adds one via
// sdktrace.WithBatcher. That keeps every internal/... package free to start
// instrumenting now without a forward dependency on the exporter choice.
func InitTracerProvider(ctx context.Context, serviceName, serviceVersion, role string) (shutdown func(context.Context) error, err error) {
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", serviceVersion),
			attribute.String("hangar.role", role),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: building otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1))),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider. Safe to call before
// InitTracerProvider — otel.Tracer falls back to a no-op provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
