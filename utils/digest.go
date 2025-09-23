package utils

import (
	"bytes"
	"encoding/gob"

	"github.com/michael112233/pbft/core"
)

func GetDigest(data *core.RequestMessage) string {
	var tmp_data = *data
	var buf bytes.Buffer
	encrypt := gob.NewEncoder(&buf)
	err := encrypt.Encode(&tmp_data)
	if err != nil {
		return ""
	}
	return string(buf.Bytes())
}