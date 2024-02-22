package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/gs2io/gs2-golang-sdk/core"
	"github.com/gs2io/gs2-golang-sdk/log"
	uuid "github.com/iris-contrib/go.uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"os"
	"os/signal"
	"time"
)

type RequestIdGenerator struct {
}

func (p RequestIdGenerator) NewIDs(
	ctx context.Context,
) (trace.TraceID, trace.SpanID) {
	v := ctx.Value("log").(log.AccessLogWithTelemetry)
	tid := func() trace.TraceID {
		if v.UserId == nil {
			return trace.TraceID(make([]byte, 16))
		}
		tid, _ := uuid.FromString(*v.UserId)
		return trace.TraceID(tid.Bytes())
	}()
	sid, _ := uuid.FromString(*v.RequestId)
	return tid, trace.SpanID(sid.Bytes())
}

func (p RequestIdGenerator) NewSpanID(
	ctx context.Context,
	traceID trace.TraceID,
) trace.SpanID {
	v := ctx.Value("log").(log.AccessLogWithTelemetry)
	sid, _ := uuid.FromString(*v.RequestId)
	return trace.SpanID(sid.Bytes())
}

func initProvider(
	host string,
	port int,
) (func(context.Context) error, error) {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("gs2"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, fmt.Sprintf("%s:%d", host, port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithIDGenerator(RequestIdGenerator{}),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tracerProvider.Shutdown, nil
}

func main() {
	clientId := flag.String("clientid", "", "gs2 client id")
	secret := flag.String("secret", "", "gs2 client secret")
	region := flag.String("region", "ap-northeast-1", "gs2 region")
	namespaceName := flag.String("namespace", "", "gs2 log namespace name")
	host := flag.String("host", "localhost", "host name of collector")
	port := flag.Int("port", 4317, "port number of collector")
	userId := flag.String("user", "", "user id")
	begin := flag.String("begin", "", "log collection begin time")
	end := flag.String("end", "", "log collection end time")

	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	shutdown, err := initProvider(
		*host,
		*port,
	)
	if err != nil {
		println(fmt.Sprintf("cannot connect open telemetry server: %s:%d", *host, *port))
		panic(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			_ = fmt.Errorf("failed to shutdown TracerProvider: %w", err)
		}
	}()

	tracer := otel.Tracer("tracer")

	session := core.Gs2RestSession{
		Credential: core.BasicGs2Credential{
			ClientId:     core.ClientId(*clientId),
			ClientSecret: core.ClientSecret(*secret),
		},
		Region: core.Region(*region),
	}
	err = session.Connect()
	if err != nil {
		panic(err)
	}

	client := log.Gs2LogRestClient{
		Session: &session,
	}
	pageToken := (*string)(nil)
	limit := int32(1000)
	scanSize := int64(0)
	rows := int64(0)
	for {
		result, err := client.QueryAccessLogWithTelemetry(
			&log.QueryAccessLogWithTelemetryRequest{
				NamespaceName: namespaceName,
				UserId:        userId,
				Begin: func() *int64 {
					t, err := time.Parse(time.RFC3339, *begin)
					if err != nil {
						panic(err)
					}
					v := t.UnixMilli()
					return &v
				}(),
				End: func() *int64 {
					t, err := time.Parse(time.RFC3339, *end)
					if err != nil {
						panic(err)
					}
					v := t.UnixMilli()
					return &v
				}(),
				PageToken: pageToken,
				Limit:     &limit,
			},
		)
		if err != nil {
			panic(err)
		}

		for _, item := range result.Items {
			event(tracer, item)
		}
		scanSize += *result.ScanSize
		rows = *result.TotalCount

		if result.NextPageToken != nil {
			pageToken = result.NextPageToken
		} else {
			break
		}
	}
	println("scanSize", byteCountIEC(scanSize), "rows", rows)
}

func byteCountIEC(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB",
		float64(b)/float64(div), "KMGTPE"[exp])
}

func event(
	tracer trace.Tracer,
	log log.AccessLogWithTelemetry,
) {
	ctx := context.WithValue(context.Background(), "log", log)

	_, span := tracer.Start(
		trace.ContextWithSpanContext(
			ctx,
			trace.SpanContext{}.
				WithTraceID(func() trace.TraceID {
					if log.UserId == nil || *log.UserId == "" {
						return trace.TraceID(make([]byte, 16))
					}
					tid, _ := uuid.FromString(*log.UserId)
					return trace.TraceID(tid.Bytes())
				}()).
				WithSpanID(func() trace.SpanID {
					if log.SourceRequestId == nil || *log.SourceRequestId == "" {
						return trace.SpanID(make([]byte, 8))
					}
					sid, _ := uuid.FromString(*log.SourceRequestId)
					return trace.SpanID(sid.Bytes())
				}()),
		),
		*log.Method,
		trace.WithTimestamp(time.UnixMilli(*log.Timestamp)),
	)
	span.SetAttributes(attribute.KeyValue{
		Key:   "requestId",
		Value: attribute.StringValue(*log.RequestId),
	})
	span.SetAttributes(attribute.KeyValue{
		Key: "userId",
		Value: attribute.StringValue(func() string {
			if log.UserId == nil {
				return ""
			}
			return *log.UserId
		}()),
	})
	span.SetAttributes(attribute.KeyValue{
		Key:   "service",
		Value: attribute.StringValue(*log.Service),
	})
	span.SetAttributes(attribute.KeyValue{
		Key:   "method",
		Value: attribute.StringValue(*log.Method),
	})
	span.SetAttributes(attribute.KeyValue{
		Key:   "request",
		Value: attribute.StringValue(*log.Request),
	})
	span.SetAttributes(attribute.KeyValue{
		Key:   "result",
		Value: attribute.StringValue(*log.Result),
	})
	if *log.Status == "ok" {
		span.SetStatus(codes.Ok, "")
	}
	if *log.Status == "warning" {
		span.SetStatus(codes.Error, "")
	}
	span.End(trace.WithTimestamp(time.UnixMilli(*log.Timestamp).Add(time.Millisecond * time.Duration(*log.Duration))))
}
