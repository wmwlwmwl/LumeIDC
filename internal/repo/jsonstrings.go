package repo

import (
	"encoding/json"
)

func decodeJSONStrings(b []byte) map[string]string {
	m := map[string]string{}
	json.Unmarshal(b, &m) // 宽松解码，坏数据返回空表
	return m
}

func encodeJSONStrings(m map[string]string) []byte {
	b, _ := json.Marshal(m)
	return b
}
