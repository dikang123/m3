package promql

import (
	"testing"

	"github.com/m3db/m3coordinator/functions"
	"github.com/m3db/m3coordinator/parser"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAG(t *testing.T) {
	q := "sum(http_requests_total{method=\"GET\"} offset 5m) by (service)"
	p, err := Parse(q)
	require.NoError(t, err)
	transforms, edges, err := p.DAG()
	require.NoError(t, err)
	assert.Len(t, transforms, 2)
	assert.Equal(t, transforms[0].Op.OpType(), functions.FetchType)
	assert.Equal(t, transforms[0].ID, parser.NodeID("0"))
	assert.Equal(t, transforms[1].ID, parser.NodeID("1"))
	assert.Len(t, edges, 1)
	assert.Equal(t, edges[0].ParentID, parser.NodeID("0"), "fetch should be the parent")
	assert.Equal(t, edges[0].ChildID, parser.NodeID("1"), "aggregation should be the child")

}