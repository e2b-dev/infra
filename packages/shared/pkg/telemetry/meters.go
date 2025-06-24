package telemetry

import "go.opentelemetry.io/otel/metric"

type (
	CounterType                 string
	GaugeFloatType              string
	GaugeIntType                string
	UpDownCounterType           string
	ObservableUpDownCounterType string
)

const (
	SandboxCreateMeterName CounterType = "api.env.instance.started"
)

const (
	SandboxCountMeterName                  UpDownCounterType = "api.env.instance.running"
	NewNetworkSlotSPoolCounterMeterName    UpDownCounterType = "orchestrator.network.slots_pool.new"
	ReusedNetworkSlotSPoolCounterMeterName UpDownCounterType = "orchestrator.network.slots_pool.reused"
	NBDkSlotSReadyPoolCounterMeterName     UpDownCounterType = "orchestrator.nbd.slots_pool.read"
)

const (
	ApiOrchestratorCountMeterName ObservableUpDownCounterType = "api.orchestrator.status"

	OrchestratorSandboxCountMeterName ObservableUpDownCounterType = "orchestrator.env.sandbox.running"

	ClientProxyServerConnectionsMeterCounterName ObservableUpDownCounterType = "client_proxy.proxy.server.connections.open"
	ClientProxyPoolConnectionsMeterCounterName   ObservableUpDownCounterType = "client_proxy.proxy.pool.connections.open"
	ClientProxyPoolSizeMeterCounterName          ObservableUpDownCounterType = "client_proxy.proxy.pool.size"

	OrchestratorProxyServerConnectionsMeterCounterName ObservableUpDownCounterType = "orchestrator.proxy.server.connections.open"
	OrchestratorProxyPoolConnectionsMeterCounterName   ObservableUpDownCounterType = "orchestrator.proxy.pool.connections.open"
	OrchestratorProxyPoolSizeMeterCounterName          ObservableUpDownCounterType = "orchestrator.proxy.pool.size"

	BuildCounterMeterName ObservableUpDownCounterType = "api.env.build.running"
)

const (
	SandboxCpuUsedGaugeName GaugeFloatType = "e2b.sandbox.cpu.used"
)

const (
	SandboxRamUsedGaugeName  GaugeIntType = "e2b.sandbox.ram.used"
	SandboxRamTotalGaugeName GaugeIntType = "e2b.sandbox.ram.total"
	SandboxCpuTotalGaugeName GaugeIntType = "e2b.sandbox.cpu.total"
)

var counterDesc = map[CounterType]string{
	SandboxCreateMeterName: "Number of currently waiting requests to create a new sandbox",
}

var counterUnits = map[CounterType]string{
	SandboxCreateMeterName: "{sandbox}",
}

var upDownCounterDesc = map[UpDownCounterType]string{
	SandboxCountMeterName:                  "Counter of started instances.",
	ReusedNetworkSlotSPoolCounterMeterName: "Number of reused network slots ready to be used.",
	NewNetworkSlotSPoolCounterMeterName:    "Number of new network slots ready to be used.",
	NBDkSlotSReadyPoolCounterMeterName:     "Number of nbd slots ready to be used.",
}

var upDownCounterUnits = map[UpDownCounterType]string{
	SandboxCountMeterName:                  "{sandbox}",
	ReusedNetworkSlotSPoolCounterMeterName: "{slot}",
	NewNetworkSlotSPoolCounterMeterName:    "{slot}",
	NBDkSlotSReadyPoolCounterMeterName:     "{slot}",
}

var observableUpDownCounterDesc = map[ObservableUpDownCounterType]string{
	ApiOrchestratorCountMeterName:                      "Counter of running orchestrators.",
	OrchestratorSandboxCountMeterName:                  "Counter of running sandboxes on the orchestrator.",
	ClientProxyServerConnectionsMeterCounterName:       "Open connections to the client proxy from load balancer.",
	ClientProxyPoolConnectionsMeterCounterName:         "Open connections from the client proxy to the orchestrator proxy.",
	ClientProxyPoolSizeMeterCounterName:                "Size of the client proxy pool.",
	OrchestratorProxyServerConnectionsMeterCounterName: "Open connections to the orchestrator proxy from client proxies.",
	OrchestratorProxyPoolConnectionsMeterCounterName:   "Open connections from the orchestrator proxy to sandboxes.",
	OrchestratorProxyPoolSizeMeterCounterName:          "Size of the orchestrator proxy pool.",
	BuildCounterMeterName:                              "Counter of running builds.",
}

var observableUpDownCounterUnits = map[ObservableUpDownCounterType]string{
	ApiOrchestratorCountMeterName:                      "{orchestrator}",
	OrchestratorSandboxCountMeterName:                  "{sandbox}",
	ClientProxyServerConnectionsMeterCounterName:       "{connection}",
	ClientProxyPoolConnectionsMeterCounterName:         "{connection}",
	ClientProxyPoolSizeMeterCounterName:                "{transport}",
	OrchestratorProxyServerConnectionsMeterCounterName: "{connection}",
	OrchestratorProxyPoolConnectionsMeterCounterName:   "{connection}",
	OrchestratorProxyPoolSizeMeterCounterName:          "{transport}",
	BuildCounterMeterName:                              "{build}",
}

var gaugeFloatDesc = map[GaugeFloatType]string{
	SandboxCpuUsedGaugeName: "Amount of CPU used by the sandbox.",
}

var gaugeFloatUnits = map[GaugeFloatType]string{
	SandboxCpuUsedGaugeName: "{percent}",
}

var gaugeIntDesc = map[GaugeIntType]string{
	SandboxRamUsedGaugeName:  "Amount of RAM used by the sandbox.",
	SandboxRamTotalGaugeName: "Amount of RAM available to the sandbox.",
	SandboxCpuTotalGaugeName: "Amount of CPU available to the sandbox.",
}

var gaugeIntUnits = map[GaugeIntType]string{
	SandboxRamUsedGaugeName:  "{By}",
	SandboxRamTotalGaugeName: "{By}",
	SandboxCpuTotalGaugeName: "{count}",
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
