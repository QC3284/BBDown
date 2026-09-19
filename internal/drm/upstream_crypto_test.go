package drm

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// 本文件搬上游 WidevineCryptoTests 的已知答案向量（RFC 4493 的 AES-CMAC 例子）：
// 加密原语是许可证解密的地基，用标准向量钉住比自造期望值可靠。

// rfc4493Key 是 RFC 4493 的标准密钥；解码失败只可能是我把常量写错，直接 panic 即可。
var rfc4493Key = func() []byte {
	b, err := hex.DecodeString("2b7e151628aed2a6abf7158809cf4f3c")
	if err != nil {
		panic(err)
	}
	return b
}()

func TestUpstreamAesCmacRfc4493(t *testing.T) {
	cases := []struct{ msgHex, wantHex string }{
		{"", "bb1d6929e95937287fa37d129b756746"},
		{"6bc1bee22e409f96e93d7e117393172a", "070a16b46b4d4144f79bdd9dd04a287c"},
		{
			"6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411",
			"dfa66747de9ae63030ca32611497c827",
		},
		{
			"6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710",
			"51f0bebf7e3b9d92fc49741779363cfe",
		},
	}
	for _, c := range cases {
		msg, err := hex.DecodeString(c.msgHex)
		if err != nil {
			t.Fatalf("hex: %v", err)
		}
		mac, err := AesCmac(rfc4493Key, msg)
		if err != nil {
			t.Fatalf("AesCmac: %v", err)
		}
		if got := hex.EncodeToString(mac); got != c.wantHex {
			t.Errorf("AES-CMAC(%q) = %s，RFC 4493 期望 %s", c.msgHex, got, c.wantHex)
		}
	}
}

func TestUpstreamAesEcbSubkeyBase(t *testing.T) {
	// RFC 4493 的子密钥 L = AES-128(key, 0^128)
	got, err := AesEcbEncrypt(make([]byte, 16), rfc4493Key)
	if err != nil {
		t.Fatalf("AesEcbEncrypt: %v", err)
	}
	if hex.EncodeToString(got) != "7df76b0c1ab899b33e42f047b91b546f" {
		t.Errorf("子密钥 = %s，RFC 4493 期望 7df76b0c1ab899b33e42f047b91b546f", hex.EncodeToString(got))
	}
	// 非 16 字节整数倍必须报错，不能静默按块处理
	if _, err := AesEcbEncrypt(make([]byte, 15), rfc4493Key); err == nil {
		t.Error("非块大小输入应报错")
	}
}

func TestUpstreamPkcs7(t *testing.T) {
	for _, n := range []int{0, 1, 15, 16, 31} {
		data := bytes.Repeat([]byte{0xAB}, n)
		padded := Pkcs7Pad(data, 16)
		if len(padded)%16 != 0 {
			t.Errorf("长度 %d 填充后不是 16 的倍数: %d", n, len(padded))
		}
		if len(padded) <= n {
			t.Errorf("长度 %d 填充后应更长", n)
		}
		unpadded, err := Pkcs7Unpad(padded)
		if err != nil {
			t.Fatalf("Pkcs7Unpad(%d): %v", n, err)
		}
		if !bytes.Equal(unpadded, data) {
			t.Errorf("长度 %d 往返不一致", n)
		}
	}

	// 畸形填充必须报错：长度与实际不符 / 长度为 0 / 空输入
	for _, bad := range [][]byte{{0x01, 0x02, 0x03}, {0x00}, {}} {
		if _, err := Pkcs7Unpad(bad); err == nil {
			t.Errorf("畸形填充 %v 应报错", bad)
		}
	}
}
