package meters

import (
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type CounterType string

const (
	SandboxCreateMeterName CounterType = "api.env.instance.started"
)

type UpDownCounterType string

const (
	SandboxCountMeterName                              UpDownCounterType = "api.env.instance.running"
	BuildCounterMeterName                              UpDownCounterType = "api.env.build.running"
	NewNetworkSlotSPoolCounterMeterName                UpDownCounterType = "orchestrator.network.slots_pool.new"
	ReusedNetworkSlotSPoolCounterMeterName             UpDownCounterType = "orchestrator.network.slots_pool.reused"
	NBDkSlotSReadyPoolCounterMeterName                 UpDownCounterType = "orchestrator.nbd.slots_pool.read"
	ActiveConnectionsCounterMeterName                  UpDownCounterType = "client_proxy.connections.active"
	OrchestratorProxyActiveConnectionsCounterMeterName UpDownCounterType = "orchestrator.proxy.connections.active"
)

type ObservableUpDownCounterType string

const (
	OrchestratorSandboxCountMeterName ObservableUpDownCounterType = "orchestrator.env.sandbox.running"
)

var meter = otel.GetMeterProvider().Meter("nomad")
var meterLock = sync.Mutex{}
var counters = make(map[CounterType]metric.Int64Counter)
var upDownCounters = make(map[UpDownCounterType]metric.Int64UpDownCounter)
var observableUpDownCounters = make(map[ObservableUpDownCounterType]metric.Int64ObservableUpDownCounter)

var counterDesc = map[CounterType]string{
	SandboxCreateMeterName: "Number of currently waiting requests to create a new sandbox",
}

var counterUnits = map[CounterType]string{
	SandboxCreateMeterName: "{sandbox}",
}

var upDownCounterDesc = map[UpDownCounterType]string{
	SandboxCountMeterName:                  "Counter of started instances.",
	BuildCounterMeterName:                  "Counter of running builds.",
	ReusedNetworkSlotSPoolCounterMeterName: "Number of reused network slots ready to be used.",
	NewNetworkSlotSPoolCounterMeterName:    "Number of new network slots ready to be used.",
	NBDkSlotSReadyPoolCounterMeterName:     "Number of nbd slots ready to be used.",
	ActiveConnectionsCounterMeterName:      "Number of active network connections in the client proxy.",
}

var upDownCounterUnits = map[UpDownCounterType]string{
	SandboxCountMeterName:                  "{sandbox}",
	BuildCounterMeterName:                  "{build}",
	ReusedNetworkSlotSPoolCounterMeterName: "{slot}",
	NewNetworkSlotSPoolCounterMeterName:    "{slot}",
	NBDkSlotSReadyPoolCounterMeterName:     "{slot}",
	ActiveConnectionsCounterMeterName:      "{connection}",
}

var observableUpDownCounterDesc = map[ObservableUpDownCounterType]string{
	OrchestratorSandboxCountMeterName: "Counter of running sandboxes on the orchestrator.",
}

var observableUpDownCounterUnits = map[ObservableUpDownCounterType]string{
	OrchestratorSandboxCountMeterName: "{sandbox}",
}

func GetCounter(name CounterType) (metric.Int64Counter, error) {
	meterLock.Lock()
	defer meterLock.Unlock()

	if counter, ok := counters[name]; ok {
		return counter, nil
	}

	counter, err := meter.Int64Counter(string(name), metric.WithDescription(counterDesc[name]), metric.WithUnit(counterUnits[name]))
	if err != nil {
		return nil, err
	}

	counters[name] = counter

	return counter, nil
}

func GetUpDownCounter(name UpDownCounterType) (metric.Int64UpDownCounter, error) {
	meterLock.Lock()
	defer meterLock.Unlock()

	if counter, ok := upDownCounters[name]; ok {
		return counter, nil
	}

	counter, err := meter.Int64UpDownCounter(string(name), metric.WithDescription(upDownCounterDesc[name]), metric.WithUnit(upDownCounterUnits[name]))
	if err != nil {
		return nil, err
	}

	upDownCounters[name] = counter

	return counter, nil
}

func GetObservableUpDownCounter(name ObservableUpDownCounterType, callback metric.Int64Callback) (metric.Int64ObservableUpDownCounter, error) {
	meterLock.Lock()
	defer meterLock.Unlock()

	if counter, ok := observableUpDownCounters[name]; ok {
		return counter, nil
	}

	counter, err := meter.Int64ObservableUpDownCounter(string(name), metric.WithDescription(observableUpDownCounterDesc[name]), metric.WithUnit(observableUpDownCounterUnits[name]), metric.WithInt64Callback(callback))
	if err != nil {
		return nil, err
	}

	observableUpDownCounters[name] = counter

	return counter, nil
}
