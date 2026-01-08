package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNfsstatParse(t *testing.T) {
	input := strings.TrimSpace(`
nfs v4 client        total:  7676067 
------------- ------------- --------
nfs v4 client         null:        2 
nfs v4 client      readdir:  1547278 
nfs v4 client  server_caps:        5 
nfs v4 client  exchange_id:        3 
nfs v4 client create_session:        1 
nfs v4 client     sequence:     1169 
nfs v4 client reclaim_comp:        1 
nfs v4 client test_stateid:        1 
nfs v4 client bind_conn_to_ses:      245 `)

	actual, err := nfsstatParse(input)
	require.NoError(t, err)

	assert.Equal(t, []nfsstat{
		{"nfs v4 client", "total", 7676067},
		{"nfs v4 client", "null", 2},
		{"nfs v4 client", "readdir", 1547278},
		{"nfs v4 client", "server_caps", 5},
		{"nfs v4 client", "exchange_id", 3},
		{"nfs v4 client", "create_session", 1},
		{"nfs v4 client", "sequence", 1169},
		{"nfs v4 client", "reclaim_comp", 1},
		{"nfs v4 client", "test_stateid", 1},
		{"nfs v4 client", "bind_conn_to_ses", 245},
	}, actual)
}
