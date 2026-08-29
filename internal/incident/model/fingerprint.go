package model

import (
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprint 根据服务与告警标识生成稳定的去重指纹。
//
// 若存在 AlertName 则使用 service+alertName 作为身份键，
// 否则回退到 service+title。相同身份键产生相同指纹，用于 Incident 去重。
func Fingerprint(service, alertName, title string) string {
	key := service + "\x00"
	if alertName != "" {
		key += alertName
	} else {
		key += title
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
