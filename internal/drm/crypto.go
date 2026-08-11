package drm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// WidevineCrypto provides cryptographic operations for Widevine DRM.
type WidevineCrypto struct{}

// AesEcbEncrypt encrypts data using AES-ECB (data must be exactly 16 bytes).
func AesEcbEncrypt(data, key []byte) ([]byte, error) {
	if len(data) != 16 {
		return nil, fmt.Errorf("ECB block must be exactly 16 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 16)
	block.Encrypt(result, data)
	return result, nil
}

// AesEcbDecrypt decrypts data using AES-ECB.
func AesEcbDecrypt(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	result := make([]byte, len(data))
	block.Decrypt(result, data)
	return result, nil
}

// AesCbcDecrypt decrypts data using AES-CBC with no padding.
func AesCbcDecrypt(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not a multiple of block size")
	}
	result := make([]byte, len(data))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(result, data)
	return result, nil
}

// AesCbcEncrypt encrypts data using AES-CBC with no padding.
func AesCbcEncrypt(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("plaintext is not a multiple of block size")
	}
	result := make([]byte, len(data))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(result, data)
	return result, nil
}

// Pkcs7Unpad removes PKCS#7 padding.
func Pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data for PKCS7 unpad")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > 16 || pad > len(data) {
		return nil, fmt.Errorf("bad PKCS7 padding")
	}
	for i := len(data) - pad; i < len(data); i++ {
		if data[i] != byte(pad) {
			return nil, fmt.Errorf("inconsistent PKCS7 padding")
		}
	}
	return data[:len(data)-pad], nil
}

// Pkcs7Pad adds PKCS#7 padding.
func Pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - (len(data) % blockSize)
	if pad == 0 {
		pad = blockSize
	}
	result := make([]byte, len(data)+pad)
	copy(result, data)
	for i := len(data); i < len(result); i++ {
		result[i] = byte(pad)
	}
	return result
}

// AesCmac computes AES-CMAC (RFC 4493).
func AesCmac(key, message []byte) ([]byte, error) {
	// 1. Generate subkeys
	zero := make([]byte, 16)
	l, err := AesEcbEncrypt(zero, key)
	if err != nil {
		return nil, err
	}
	k1 := subKey(l)
	k2 := subKey(k1)

	// 2. Calculate number of blocks
	n := (len(message) + 15) / 16
	if n == 0 {
		n = 1
	}
	lastComplete := len(message) > 0 && len(message)%16 == 0

	// 3. Construct last block
	lastBlock := make([]byte, 16)
	if lastComplete {
		start := len(message) - 16
		copy(lastBlock, message[start:])
		xorInPlace(lastBlock, k1)
	} else {
		partialLen := len(message) % 16
		start := len(message) - partialLen
		copy(lastBlock, message[start:])
		lastBlock[partialLen] = 0x80
		xorInPlace(lastBlock, k2)
	}

	// 4. Iterate encryption
	x := make([]byte, 16)
	for i := 0; i < n-1; i++ {
		block := make([]byte, 16)
		copy(block, message[i*16:(i+1)*16])
		xorInPlace(x, block)
		x, err = AesEcbEncrypt(x, key)
		if err != nil {
			return nil, err
		}
	}

	xorInPlace(x, lastBlock)
	return AesEcbEncrypt(x, key)
}

func subKey(key []byte) []byte {
	result := make([]byte, 16)
	var carry byte
	for i := 15; i >= 0; i-- {
		b := (int(key[i]) << 1) | int(carry)
		result[i] = byte(b)
		if key[i]&0x80 != 0 {
			carry = 1
		} else {
			carry = 0
		}
	}
	if key[0]&0x80 != 0 {
		result[15] ^= 0x87
	}
	return result
}

func xorInPlace(a, b []byte) {
	for i := 0; i < len(a); i++ {
		a[i] ^= b[i]
	}
}

// DeriveContext derives Widevine encryption and MAC contexts from a challenge message.
func DeriveContext(message []byte) (encContext, macContext []byte) {
	encLabel := []byte("ENCRYPTION\x00")
	macLabel := []byte("AUTHENTICATION\x00")

	encContext = make([]byte, len(encLabel)+len(message)+4)
	copy(encContext, encLabel)
	copy(encContext[len(encLabel):], message)
	// key_size = 128 bits = 16 bytes → big-endian
	binary.BigEndian.PutUint32(encContext[len(encContext)-4:], 0x80)

	macContext = make([]byte, len(macLabel)+len(message)+4)
	copy(macContext, macLabel)
	copy(macContext[len(macLabel):], message)
	// key_size = 512 bits = 64 bytes → big-endian
	binary.BigEndian.PutUint32(macContext[len(macContext)-4:], 0x200)

	return
}

// DeriveKeys derives encryption and MAC keys from a session key.
func DeriveKeys(sessionKey, encContext, macContext []byte) (encKey, macKeyServer, macKeyClient []byte, err error) {
	derive := func(context []byte, counter byte) ([]byte, error) {
		input := make([]byte, 1+len(context))
		input[0] = counter
		copy(input[1:], context)
		return AesCmac(sessionKey, input)
	}

	encKey, err = derive(encContext, 1)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("derive enc key: %w", err)
	}

	macKeyServer1, err := derive(macContext, 1)
	if err != nil {
		return nil, nil, nil, err
	}
	macKeyServer2, err := derive(macContext, 2)
	if err != nil {
		return nil, nil, nil, err
	}
	macKeyServer = make([]byte, len(macKeyServer1)+len(macKeyServer2))
	copy(macKeyServer, macKeyServer1)
	copy(macKeyServer[len(macKeyServer1):], macKeyServer2)

	macKeyClient1, err := derive(macContext, 3)
	if err != nil {
		return nil, nil, nil, err
	}
	macKeyClient2, err := derive(macContext, 4)
	if err != nil {
		return nil, nil, nil, err
	}
	macKeyClient = make([]byte, len(macKeyClient1)+len(macKeyClient2))
	copy(macKeyClient, macKeyClient1)
	copy(macKeyClient[len(macKeyClient1):], macKeyClient2)

	return
}

// HmacSHA256 computes HMAC-SHA256.
func HmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
