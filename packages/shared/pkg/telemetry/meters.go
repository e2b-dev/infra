package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type (
	CounterType                 string
	ObservableCounterType       string
	GaugeFloatType              string
	GaugeIntType                string
	UpDownCounterType           string
	ObservableUpDownCounterType string
	HistogramType               string
)

const (
	ApiOrchestratorCreatedSandboxes CounterType = "api.orchestrator.created_sandboxes"
	SandboxCreateMeterName          CounterType = "api.env.instance.started"

	TeamSandboxCreated CounterType = "e2b.team.sandbox.created"

	EnvdInitCalls CounterType = "orchestrator.sandbox.envd.init.calls"
)

const (
	ApiOrchestratorSbxCreateSuccess ObservableCounterType = "api.orchestrator.sandbox.create.success"
	ApiOrchestratorSbxCreateFailure ObservableCounterType = "api.orchestrator.sandbox.create.failure"
)

const (
	SandboxCountMeterName UpDownCounterType = "api.env.instance.running"
)

const (
	OrchestratorSandboxCountMeterName ObservableUpDownCounterType = "orchestrator.env.sandbox.running"

	ClientProxyServerConnectionsMeterCounterName ObservableUpDownCounterType = "client_proxy.proxy.server.connections.open"
	ClientProxyPoolConnectionsMeterCounterName   ObservableUpDownCounterType = "client_proxy.proxy.pool.connections.open"
	ClientProxyPoolSizeMeterCounterName          ObservableUpDownCounterType = "client_proxy.proxy.pool.size"

	OrchestratorProxyServerConnectionsMeterCounterName ObservableUpDownCounterType = "orchestrator.proxy.server.connections.open"
	OrchestratorProxyPoolConnectionsMeterCounterName   ObservableUpDownCounterType = "orchestrator.proxy.pool.connections.open"
	OrchestratorProxyPoolSizeMeterCounterName          ObservableUpDownCounterType = "orchestrator.proxy.pool.size"

	BuildCounterMeterName ObservableUpDownCounterType = "api.env.build.running"

	TCPFirewallActiveConnections ObservableUpDownCounterType = "orchestrator.tcpfirewall.connections.active"
)

const (
	SandboxCpuUsedGaugeName GaugeFloatType = "e2b.sandbox.cpu.used"
)

const (
	// Build timing histograms
	BuildDurationHistogramName      HistogramType = "template.build.duration"
	BuildPhaseDurationHistogramName HistogramType = "template.build.phase.duration"
	BuildStepDurationHistogramName  HistogramType = "template.build.step.duration"

	// Sandbox timing histograms
	WaitForEnvdDurationHistogramName HistogramType = "orchestrator.sandbox.envd.init.duration"

	// TCP Firewall histograms
	TCPFirewallConnectionDurationHistogramName    HistogramType = "orchestrator.tcpfirewall.connection.duration"
	TCPFirewallConnectionsPerSandboxHistogramName HistogramType = "orchestrator.tcpfirewall.connections.per_sandbox"
)

const (
	// Build result counters
	BuildResultCounterName      CounterType = "template.build.result"
	BuildCacheResultCounterName CounterType = "template.build.cache.result"

	// TCP Firewall counters
	TCPFirewallConnectionsTotal CounterType = "orchestrator.tcpfirewall.connections.total"
	TCPFirewallErrorsTotal      CounterType = "orchestrator.tcpfirewall.errors.total"
	TCPFirewallDecisionsTotal   CounterType = "orchestrator.tcpfirewall.decisions.total"
)

const (
	ApiOrchestratorCountMeterName GaugeIntType = "api.orchestrator.status"

	// Sandbox metrics
	SandboxRamUsedGaugeName   GaugeIntType = "e2b.sandbox.ram.used"
	SandboxRamTotalGaugeName  GaugeIntType = "e2b.sandbox.ram.total"
	SandboxCpuTotalGaugeName  GaugeIntType = "e2b.sandbox.cpu.total"
	SandboxDiskUsedGaugeName  GaugeIntType = "e2b.sandbox.disk.used"
	SandboxDiskTotalGaugeName GaugeIntType = "e2b.sandbox.disk.total"

	// Team metrics
	TeamSandboxRunningGaugeName GaugeIntType = "e2b.team.sandbox.running"

	// Build resource metrics
	BuildRootfsSizeHistogramName HistogramType = "template.build.rootfs.size"
)

var counterDesc = map[CounterType]string{
	SandboxCreateMeterName:          "Number of currently waiting requests to create a new sandbox",
	ApiOrchestratorCreatedSandboxes: "Number of successfully created sandboxes",
	BuildResultCounterName:          "Number of template build results",
	BuildCacheResultCounterName:     "Number of build cache results",
	TeamSandboxCreated:              "Counter of started sandboxes for the team in the interval",
	EnvdInitCalls:                   "Number of envd initialization calls",

	TCPFirewallConnectionsTotal: "Total number of TCP firewall connections processed",
	TCPFirewallErrorsTotal:      "Total number of TCP firewall errors",
	TCPFirewallDecisionsTotal:   "Total number of TCP firewall allow/block decisions",
}

var counterUnits = map[CounterType]string{
	SandboxCreateMeterName:          "{sandbox}",
	ApiOrchestratorCreatedSandboxes: "{sandbox}",
	BuildResultCounterName:          "{build}",
	BuildCacheResultCounterName:     "{layer}",
	TeamSandboxCreated:              "{sandbox}",
	EnvdInitCalls:                   "1",

	TCPFirewallConnectionsTotal: "{connection}",
	TCPFirewallErrorsTotal:      "{error}",
	TCPFirewallDecisionsTotal:   "{decision}",
}

var observableCounterDesc = map[ObservableCounterType]string{
	ApiOrchestratorSbxCreateSuccess: "Counter of successful sandbox creation requests.",
	ApiOrchestratorSbxCreateFailure: "Counter of failed sandbox creation requests.",
}

var observableCounterUnits = map[ObservableCounterType]string{
	ApiOrchestratorSbxCreateSuccess: "{sandbox}",
	ApiOrchestratorSbxCreateFailure: "{sandbox}",
}

var upDownCounterDesc = map[UpDownCounterType]string{
	SandboxCountMeterName: "Counter of started instances.",
}

var upDownCounterUnits = map[UpDownCounterType]string{
	SandboxCountMeterName: "{sandbox}",
}

var observableUpDownCounterDesc = map[ObservableUpDownCounterType]string{
	OrchestratorSandboxCountMeterName:                  "Counter of running sandboxes on the orchestrator.",
	ClientProxyServerConnectionsMeterCounterName:       "Open connections to the client proxy from load balancer.",
	ClientProxyPoolConnectionsMeterCounterName:         "Open connections from the client proxy to the orchestrator proxy.",
	ClientProxyPoolSizeMeterCounterName:                "Size of the client proxy pool.",
	OrchestratorProxyServerConnectionsMeterCounterName: "Open connections to the orchestrator proxy from client proxies.",
	OrchestratorProxyPoolConnectionsMeterCounterName:   "Open connections from the orchestrator proxy to sandboxes.",
	OrchestratorProxyPoolSizeMeterCounterName:          "Size of the orchestrator proxy pool.",
	BuildCounterMeterName:                              "Counter of running builds.",

	TCPFirewallActiveConnections: "Number of currently active TCP firewall connections.",
}

var observableUpDownCounterUnits = map[ObservableUpDownCounterType]string{
	OrchestratorSandboxCountMeterName:                  "{sandbox}",
	ClientProxyServerConnectionsMeterCounterName:       "{connection}",
	ClientProxyPoolConnectionsMeterCounterName:         "{connection}",
	ClientProxyPoolSizeMeterCounterName:                "{transport}",
	OrchestratorProxyServerConnectionsMeterCounterName: "{connection}",
	OrchestratorProxyPoolConnectionsMeterCounterName:   "{connection}",
	OrchestratorProxyPoolSizeMeterCounterName:          "{transport}",
	BuildCounterMeterName:                              "{build}",

	TCPFirewallActiveConnections: "{connection}",
}

var gaugeFloatDesc = map[GaugeFloatType]string{
	SandboxCpuUsedGaugeName: "Amount of CPU used by the sandbox.",
}

var gaugeFloatUnits = map[GaugeFloatType]string{
	SandboxCpuUsedGaugeName: "{percent}",
}

var gaugeIntDesc = map[GaugeIntType]string{
	ApiOrchestratorCountMeterName: "Counter of running orchestrators.",
	SandboxRamUsedGaugeName:       "Amount of RAM used by the sandbox.",
	SandboxRamTotalGaugeName:      "Amount of RAM available to the sandbox.",
	SandboxCpuTotalGaugeName:      "Amount of CPU available to the sandbox.",
	SandboxDiskUsedGaugeName:      "Amount of disk space used by the sandbox.",
	SandboxDiskTotalGaugeName:     "Amount of disk space available to the sandbox.",
	TeamSandboxRunningGaugeName:   "The number of sandboxes running for the team in the interval.",
}

var gaugeIntUnits = map[GaugeIntType]string{
	ApiOrchestratorCountMeterName: "{orchestrator}",
	SandboxRamUsedGaugeName:       "{By}",
	SandboxRamTotalGaugeName:      "{By}",
	SandboxCpuTotalGaugeName:      "{count}",
	SandboxDiskUsedGaugeName:      "{By}",
	SandboxDiskTotalGaugeName:     "{By}",
	TeamSandboxRunningGaugeName:   "{sandbox}",
}

func GetCounter(meter metric.Meter, name CounterType) (metric.Int64Counter, error) {
	desc := counterDesc[name]
	unit := counterUnits[name]

	return meter.Int64Counter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetUpDownCounter(meter metric.Meter, name UpDownCounterType) (metric.Int64UpDownCounter, error) {
	desc := upDownCounterDesc[name]
	unit := upDownCounterUnits[name]

	return meter.Int64UpDownCounter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetObservableCounter(meter metric.Meter, name ObservableCounterType, callback metric.Int64Callback) (metric.Int64ObservableCounter, error) {
	desc := observableCounterDesc[name]
	unit := observableCounterUnits[name]

	return meter.Int64ObservableCounter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
		metric.WithInt64Callback(callback),
	)
}

func GetObservableUpDownCounter(meter metric.Meter, name ObservableUpDownCounterType, callback metric.Int64Callback) (metric.Int64ObservableUpDownCounter, error) {
	desc := observableUpDownCounterDesc[name]
	unit := observableUpDownCounterUnits[name]

	return meter.Int64ObservableUpDownCounter(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
		metric.WithInt64Callback(callback),
	)
}

func GetGaugeFloat(meter metric.Meter, name GaugeFloatType) (metric.Float64ObservableGauge, error) {
	desc := gaugeFloatDesc[name]
	unit := gaugeFloatUnits[name]

	return meter.Float64ObservableGauge(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

func GetGaugeInt(meter metric.Meter, name GaugeIntType) (metric.Int64ObservableGauge, error) {
	desc := gaugeIntDesc[name]
	unit := gaugeIntUnits[name]

	return meter.Int64ObservableGauge(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

var histogramDesc = map[HistogramType]string{
	BuildDurationHistogramName:       "Time taken to build a template",
	BuildPhaseDurationHistogramName:  "Time taken to build each phase of a template",
	BuildStepDurationHistogramName:   "Time taken to build each step of a template",
	BuildRootfsSizeHistogramName:     "Size of the built template rootfs in bytes",
	WaitForEnvdDurationHistogramName: "Time taken for Envd to initialize successfully",

	TCPFirewallConnectionDurationHistogramName:    "Duration of TCP firewall proxied connections",
	TCPFirewallConnectionsPerSandboxHistogramName: "Number of active TCP firewall connections per sandbox",
}

var histogramUnits = map[HistogramType]string{
	BuildDurationHistogramName:                    "ms",
	BuildPhaseDurationHistogramName:               "ms",
	BuildStepDurationHistogramName:                "ms",
	BuildRootfsSizeHistogramName:                  "{By}",
	WaitForEnvdDurationHistogramName:              "ms",
	TCPFirewallConnectionDurationHistogramName:    "ms",
	TCPFirewallConnectionsPerSandboxHistogramName: "{connection}",
}

func GetHistogram(meter metric.Meter, name HistogramType) (metric.Int64Histogram, error) {
	desc := histogramDesc[name]
	unit := histogramUnits[name]

	return meter.Int64Histogram(string(name),
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	)
}

type TimerFactory struct {
	duration metric.Int64Histogram
	bytes    metric.Int64Counter
	count    metric.Int64Counter
}

func NewTimerFactory(
	blocksMeter metric.Meter,
	metricName, durationDescription, bytesDescription, counterDescription string,
) (TimerFactory, error) {
	duration, err := blocksMeter.Int64Histogram(metricName,
		metric.WithDescription(durationDescription),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to get slices metric: %w", err)
	}

	bytes, err := blocksMeter.Int64Counter(metricName,
		metric.WithDescription(bytesDescription),
		metric.WithUnit("By"),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to create total bytes requested metric: %w", err)
	}

	count, err := blocksMeter.Int64Counter(metricName,
		metric.WithDescription(counterDescription),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to create total page faults metric: %w", err)
	}

	return TimerFactory{duration, bytes, count}, nil
}

func (f *TimerFactory) Begin(kv ...attribute.KeyValue) *Stopwatch {
	return &Stopwatch{
		histogram: f.duration,
		sum:       f.bytes,
		count:     f.count,
		start:     time.Now(),
		kv:        kv,
	}
}

type Stopwatch struct {
	histogram  metric.Int64Histogram
	sum, count metric.Int64Counter
	start      time.Time
	kv         []attribute.KeyValue
}

const (
	resultAttr        = "result"
	resultTypeSuccess = "success"
	resultTypeFailure = "failure"
)

func (t Stopwatch) Success(ctx context.Context, total int64, kv ...attribute.KeyValue) {
	t.end(ctx, resultTypeSuccess, total, kv...)
}

func (t Stopwatch) Failure(ctx context.Context, total int64, kv ...attribute.KeyValue) {
	t.end(ctx, resultTypeFailure, total, kv...)
}

func (t Stopwatch) end(ctx context.Context, result string, total int64, kv ...attribute.KeyValue) {
	kv = append(kv, attribute.KeyValue{Key: resultAttr, Value: attribute.StringValue(result)})
	kv = append(t.kv, kv...)

	amount := time.Since(t.start).Milliseconds()
	t.histogram.Record(ctx, amount, metric.WithAttributes(kv...))
	t.sum.Add(ctx, total, metric.WithAttributes(kv...))
	t.count.Add(ctx, 1, metric.WithAttributes(kv...))
}
