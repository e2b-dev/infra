package block

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockDevice(t *testing.T) {
	data := []byte("Hello, World!")
	device := NewMockDevice(data)

	// Test ReadAt
	buf := make([]byte, 5)
	n, err := device.ReadAt(buf, 0)
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("Hello"), buf)

	// Test WriteAt
	writeData := []byte("Ahoy!")
	n, err = device.WriteAt(writeData, 0)
	assert.NoError(t, err)

	assert.Equal(t, len(writeData), n)
	assert.Equal(t, []byte("Ahoy!, World!"), device.data)
}
