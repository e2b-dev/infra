package telemetry

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func WithSandboxID(sandboxID string) attribute.KeyValue {
	return zapFieldToOTELAttribute(logger.WithSandboxID(sandboxID))
}

func WithTemplateID(templateID string) attribute.KeyValue {
	return zapFieldToOTELAttribute(logger.WithTemplateID(templateID))
}

func WithBuildID(buildID string) attribute.KeyValue {
	return zapFieldToOTELAttribute(logger.WithBuildID(buildID))
}

func WithTeamID(teamID string) attribute.KeyValue {
	return zapFieldToOTELAttribute(logger.WithTeamID(teamID))
}

func zapFieldToOTELAttribute(f zap.Field) attribute.KeyValue {
	e := &ZapFieldToOTELAttributeEncoder{}
	f.AddTo(e)
	return e.KeyValue
}

type ZapFieldToOTELAttributeEncoder struct {
	attribute.KeyValue
}

func (z *ZapFieldToOTELAttributeEncoder) AddArray(key string, marshaler zapcore.ArrayMarshaler) error {
	return nil
}

func (z *ZapFieldToOTELAttributeEncoder) AddObject(key string, marshaler zapcore.ObjectMarshaler) error {
	return nil
}

func (z *ZapFieldToOTELAttributeEncoder) AddBinary(key string, value []byte) {
	z.KeyValue = attribute.String(key, fmt.Sprintf("%x", value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddByteString(key string, value []byte) {
	z.KeyValue = attribute.String(key, string(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddBool(key string, value bool) {
	z.KeyValue = attribute.Bool(key, value)
}

func (z *ZapFieldToOTELAttributeEncoder) AddComplex128(key string, value complex128) {
	z.KeyValue = attribute.String(key, fmt.Sprintf("%v", value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddComplex64(key string, value complex64) {
	z.KeyValue = attribute.String(key, fmt.Sprintf("%v", value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddDuration(key string, value time.Duration) {
	z.KeyValue = attribute.Int64(key, value.Microseconds())
}

func (z *ZapFieldToOTELAttributeEncoder) AddFloat64(key string, value float64) {
	z.KeyValue = attribute.Float64(key, value)
}

func (z *ZapFieldToOTELAttributeEncoder) AddFloat32(key string, value float32) {
	z.KeyValue = attribute.Float64(key, float64(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddInt(key string, value int) {
	z.KeyValue = attribute.Int(key, value)
}

func (z *ZapFieldToOTELAttributeEncoder) AddInt64(key string, value int64) {
	z.KeyValue = attribute.Int64(key, value)
}

func (z *ZapFieldToOTELAttributeEncoder) AddInt32(key string, value int32) {
	z.KeyValue = attribute.Int(key, int(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddInt16(key string, value int16) {
	z.KeyValue = attribute.Int(key, int(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddInt8(key string, value int8) {
	z.KeyValue = attribute.Int(key, int(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddString(key, value string) {
	z.KeyValue = attribute.String(key, value)
}

func (z *ZapFieldToOTELAttributeEncoder) AddTime(key string, value time.Time) {
	z.KeyValue = attribute.String(key, value.String())
}

func (z *ZapFieldToOTELAttributeEncoder) AddUint(key string, value uint) {
	z.KeyValue = attribute.Int64(key, int64(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddUint64(key string, value uint64) {
	asInt64 := int64(value)
	if asInt64 > 0 {
		z.KeyValue = attribute.Int64(key, asInt64)
	} else {
		z.KeyValue = attribute.String(key, "<uint64 overflow>")
	}
}

func (z *ZapFieldToOTELAttributeEncoder) AddUint32(key string, value uint32) {
	z.KeyValue = attribute.Int64(key, int64(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddUint16(key string, value uint16) {
	z.KeyValue = attribute.Int(key, int(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddUint8(key string, value uint8) {
	z.KeyValue = attribute.Int(key, int(value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddUintptr(key string, value uintptr) {
	z.KeyValue = attribute.String(key, fmt.Sprintf("%v", value))
}

func (z *ZapFieldToOTELAttributeEncoder) AddReflected(key string, value interface{}) error {
	z.KeyValue = attribute.String(key, fmt.Sprintf("%v", value))
	return nil
}

func (z *ZapFieldToOTELAttributeEncoder) OpenNamespace(key string) {
}
