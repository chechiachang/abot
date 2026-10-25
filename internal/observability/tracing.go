package observability

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func Start(ctx context.Context, endpoint, token string) (func(context.Context) error, error) {
	if strings.TrimSpace(token) == "" {
		return func(context.Context) error { return nil }, nil
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("Logfire endpoint is required when LOGFIRE_TOKEN is set")
	}
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithHeaders(map[string]string{"Authorization": "Bearer " + token}),
		otlptracehttp.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, errors.New("could not initialize Logfire OTLP exporter")
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second), sdktrace.WithExportTimeout(5*time.Second)),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "abot"))),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
