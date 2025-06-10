package metrics

import (
	"go.opentelemetry.io/otel/metric"
)

type GaugeFloatType string
type GaugeIntType string

const (
	SandboxCpuUsedGaugeName GaugeFloatType = "e2b.sandbox.cpu.used"
)

const (
	SandboxRamUsedGaugeName  GaugeIntType = "e2b.sandbox.ram.used"
	SandboxRamTotalGaugeName GaugeIntType = "e2b.sandbox.ram.total"
	SandboxCpuTotalGaugeName GaugeIntType = "e2b.sandbox.cpu.total"
)

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
	// TODO: is mebibyte small enough? shouldn't we just log bytes?
	SandboxRamUsedGaugeName:  "{mebibyte}",
	SandboxRamTotalGaugeName: "{mebibyte}",
	SandboxCpuTotalGaugeName: "{count}",
}

func (mp *MeterProvider) getGaugeFloat(name GaugeFloatType) (metric.Float64ObservableGauge, error) {
	mp.meterLock.Lock()
	defer mp.meterLock.Unlock()

	if gauge, ok := mp.gaugesFloat[name]; ok {
		return gauge, nil
	}

	gauge, err := mp.meter.Float64ObservableGauge(string(name), metric.WithDescription(gaugeFloatDesc[name]), metric.WithUnit(gaugeFloatUnits[name]))
	if err != nil {
		return nil, err
	}

	mp.gaugesFloat[name] = gauge

	return gauge, nil
}

func (mp *MeterProvider) getGaugeInt(name GaugeIntType) (metric.Int64ObservableGauge, error) {
	mp.meterLock.Lock()
	defer mp.meterLock.Unlock()

	if gauge, ok := mp.gaugesInt[name]; ok {
		return gauge, nil
	}

	gauge, err := mp.meter.Int64ObservableGauge(string(name), metric.WithDescription(gaugeIntDesc[name]), metric.WithUnit(gaugeIntUnits[name]))
	if err != nil {
		return nil, err
	}

	mp.gaugesInt[name] = gauge

	return gauge, nil
}
