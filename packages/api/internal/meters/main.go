package meters

import (
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.GetMeterProvider().Meter("nomad")
var meterLock = sync.Mutex{}
var counters = make(map[string]metric.Int64Counter)
var upDownCounters = make(map[string]metric.Int64UpDownCounter)

func CreateCounter(name, desc, unit string) error {
	meterLock.Lock()
	defer meterLock.Unlock()

	if _, exists := counters[name]; exists {
		return fmt.Errorf("counter %s already exists", name)
	}

	counter, err := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		return err
	}

	counters[name] = counter
	return nil
}

func GetCounter(name string) (metric.Int64Counter, error) {
	meterLock.Lock()
	defer meterLock.Unlock()

	if counter, ok := counters[name]; ok {
		return counter, nil
	}

	return nil, fmt.Errorf("counter %s does not exist", name)
}

func CreateUpDownCounter(name, desc, unit string) error {
	meterLock.Lock()
	defer meterLock.Unlock()

	if _, exists := upDownCounters[name]; exists {
		return fmt.Errorf("counter %s already exists", name)
	}

	counter, err := meter.Int64UpDownCounter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		return err
	}

	upDownCounters[name] = counter
	return nil
}

func GetUpDownCounter(name string) (metric.Int64UpDownCounter, error) {
	meterLock.Lock()
	defer meterLock.Unlock()

	if counter, ok := upDownCounters[name]; ok {
		return counter, nil
	}

	return nil, fmt.Errorf("counter %s does not exist", name)
}
