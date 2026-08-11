package drm

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WvdDevice represents a Widevine device loaded from a .wvd file.
type WvdDevice struct {
	ClientIDBytes        []byte
	Rsa                  *rsa.PrivateKey
	ClientIdentification []byte // raw protobuf bytes
}

// LoadWvdDevice loads a Widevine device from a .wvd file.
func LoadWvdDevice(path string) (*WvdDevice, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read wvd file: %w", err)
	}

	// Format 1: "WVD" magic header (first 3 bytes)
	if len(data) >= 4 && data[0] == 'W' && data[1] == 'V' && data[2] == 'D' {
		return parseWvd(data[3:])
	}

	// Format 2: pywidevine v1 standard format (version byte = 1)
	if len(data) >= 1 && data[0] == 1 {
		return parseWvd(data)
	}

	// Format 3: PEM private key + companion client_id blob
	if len(data) > 0 && data[0] == '-' {
		return parsePemPlusClientID(path, data)
	}

	return nil, fmt.Errorf("无法识别的 WVD 文件格式 (首字节: %d)", data[0])
}

func parseWvd(data []byte) (*WvdDevice, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("WVD data too short")
	}

	version := data[0]
	if version != 1 && version != 2 {
		return nil, fmt.Errorf("unsupported WVD version: %d", version)
	}

	// V2 with encrypted private key not supported
	if version == 2 && (data[3]&0x01) != 0 {
		return nil, fmt.Errorf("encrypted WVD V2 private key not supported")
	}

	privateKeyLen := int(binary.BigEndian.Uint16(data[4:6]))
	privateKeyBytes := data[6 : 6+privateKeyLen]
	offset := 6 + privateKeyLen

	clientIDLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	clientIDBytes := data[offset+2 : offset+2+clientIDLen]

	return createDevice(privateKeyBytes, clientIDBytes)
}

func parsePemPlusClientID(wvdPath string, privateKeyPEM []byte) (*WvdDevice, error) {
	dir := filepath.Dir(wvdPath)
	baseName := strings.TrimSuffix(filepath.Base(wvdPath), filepath.Ext(wvdPath))

	var clientIDBytes []byte
	candidates := []string{
		filepath.Join(dir, baseName+"_client_id.bin"),
		filepath.Join(dir, baseName+".client_id"),
		filepath.Join(dir, "client_id.bin"),
	}
	for _, c := range candidates {
		if data, err := os.ReadFile(c); err == nil {
			clientIDBytes = data
			break
		}
	}
	if clientIDBytes == nil {
		return nil, fmt.Errorf("PEM 格式需要配套的 client_id 文件 (_client_id.bin)")
	}

	return createDevice(privateKeyPEM, clientIDBytes)
}

func createDevice(privateKeyBytes, clientIDBytes []byte) (*WvdDevice, error) {
	rsaKey, err := importPrivateKey(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("import private key: %w", err)
	}

	return &WvdDevice{
		ClientIDBytes:        clientIDBytes,
		Rsa:                  rsaKey,
		ClientIdentification: clientIDBytes,
	}, nil
}

// importPrivateKey imports RSA private key from DER or PEM bytes.
func importPrivateKey(data []byte) (*rsa.PrivateKey, error) {
	// Check if it looks like PEM text
	if len(data) > 10 && string(data[:10]) == "-----BEGIN" {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("failed to decode PEM block")
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse PEM private key: %w", err)
			}
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA")
		}
		return rsaKey, nil
	}

	// Binary DER: try PKCS#1 first, then PKCS#8
	key, err := x509.ParsePKCS1PrivateKey(data)
	if err == nil {
		return key, nil
	}

	key8, err := x509.ParsePKCS8PrivateKey(data)
	if err == nil {
		rsaKey, ok := key8.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA")
		}
		return rsaKey, nil
	}

	// Last resort: try as base64 PEM body
	body := string(data)
	pemData := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----"
	block, _ := pem.Decode([]byte(pemData))
	if block != nil {
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return key, nil
	}

	return nil, fmt.Errorf("failed to import private key: %w", err)
}
