// Package moq TLS 辅助: 自签名证书生成 (开发/测试用)。
package moq

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// ecdsaGenerateKey 生成 P-256 ECDSA 密钥对。
func ecdsaGenerateKey() (*ecdsa.PrivateKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ecdsa key: %w", err)
	}
	return priv, nil
}

// encodeECDSAKey 将 ECDSA 私钥编码为 PEM 格式 (PKCS8)。
func encodeECDSAKey(priv *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal pkcs8: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}
