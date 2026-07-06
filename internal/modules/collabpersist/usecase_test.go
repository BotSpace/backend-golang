package collabpersist

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestApplyOp_NodeAddMoveUpdateAndDelete(t *testing.T) {
	raw := json.RawMessage(`{"nodes":[],"edges":[],"languages":["uz"],"defaultLanguage":"uz"}`)

	next, changed, err := applyOp(raw, map[string]any{
		"t": "na",
		"node": map[string]any{
			"id":       "n1",
			"type":     "SendTextMessageNode",
			"position": map[string]any{"x": float64(1), "y": float64(2)},
			"data":     map[string]any{"messageText": "old"},
		},
	})
	require.NoError(t, err)
	require.True(t, changed)

	next, changed, err = applyOp(next, map[string]any{
		"t":    "nu",
		"id":   "n1",
		"data": map[string]any{"messageText": "new"},
	})
	require.NoError(t, err)
	require.True(t, changed)

	next, changed, err = applyOp(next, map[string]any{
		"t":        "nm",
		"id":       "n1",
		"position": map[string]any{"x": float64(10), "y": float64(20)},
	})
	require.NoError(t, err)
	require.True(t, changed)

	next, changed, err = applyOp(next, map[string]any{
		"t": "ea",
		"edge": map[string]any{
			"id":     "e1",
			"source": "n1",
			"target": "n2",
		},
	})
	require.NoError(t, err)
	require.True(t, changed)

	next, changed, err = applyOp(next, map[string]any{"t": "nd", "id": "n1"})
	require.NoError(t, err)
	require.True(t, changed)

	var flow map[string]any
	require.NoError(t, json.Unmarshal(next, &flow))
	require.Equal(t, []any{}, flow["nodes"])
	require.Equal(t, []any{}, flow["edges"])
	require.Equal(t, []any{"uz"}, flow["languages"])
}

func TestDecodeOp_MsgpackDeflateBase64(t *testing.T) {
	op := map[string]any{"t": "nd", "id": "n1"}
	body, err := msgpack.Marshal(op)
	require.NoError(t, err)

	var compressed bytes.Buffer
	w, err := flate.NewWriter(&compressed, flate.DefaultCompression)
	require.NoError(t, err)
	_, err = w.Write(body)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	got, err := decodeOp(base64.StdEncoding.EncodeToString(compressed.Bytes()))
	require.NoError(t, err)
	require.Equal(t, "nd", got["t"])
	require.Equal(t, "n1", got["id"])
}
