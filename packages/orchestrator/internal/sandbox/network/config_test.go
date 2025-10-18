package network

import (
	"net"
	"reflect"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIPNet(t *testing.T) {
	t.Setenv("CIDR_OVERRIDE_DEFAULT", "5.0.0.0/8")

	var testConfig struct {
		HasDefault           *net.IPNet `env:"CIDR_USE_DEFAULT"             envDefault:"10.10.0.0/16"`
		OverrideDefault      *net.IPNet `env:"CIDR_OVERRIDE_DEFAULT"        envDefault:"1.2.3.4/16"`
		NilBecauseNonePassed *net.IPNet `env:"CIDR_NIL_BECAUSE_NONE_PASSED"`
	}

	err := env.ParseWithOptions(&testConfig, env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeOf(net.IPNet{}): ParseIPNet,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "10.10.0.0/16", testConfig.HasDefault.String())
	assert.Equal(t, "5.0.0.0/8", testConfig.OverrideDefault.String())
	assert.Nil(t, testConfig.NilBecauseNonePassed)
}
