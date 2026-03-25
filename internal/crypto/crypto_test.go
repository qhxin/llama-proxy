package crypto

import (
	"bytes"
	"testing"
)

func TestCrypto_EncryptAndDecrypt(t *testing.T) {
	c, err := New("test-secret-key-for-encryption-32-bytes!")
	if err != nil {
		t.Fatalf("Failed to create crypto: %v", err)
	}
	defer c.Close()

	testData := []byte(`{"model":"llama2","messages":[{"role":"user","content":"Hello"}],"stream":true}`)

	// 加密
	encrypted, err := c.EncryptAndCompress(testData)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}

	// 验证加密后数据变长了（有头部开销）
	if len(encrypted) <= len(testData) {
		t.Logf("Compressed data size: %d, original: %d", len(encrypted), len(testData))
	}

	// 解密
	decrypted, err := c.DecryptAndDecompress(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt: %v", err)
	}

	// 验证解密后数据一致
	if !bytes.Equal(testData, decrypted) {
		t.Errorf("Decrypted data doesn't match original\nOriginal: %s\nDecrypted: %s", testData, decrypted)
	}
}

func TestCrypto_InvalidKey(t *testing.T) {
	_, err := NewWithKey([]byte("short"))
	if err != ErrInvalidKey {
		t.Errorf("Expected ErrInvalidKey, got: %v", err)
	}
}

func TestCrypto_InvalidCiphertext(t *testing.T) {
	c, err := New("test-secret-key-for-encryption-32-bytes!")
	if err != nil {
		t.Fatalf("Failed to create crypto: %v", err)
	}
	defer c.Close()

	// 测试过短的数据
	_, err = c.DecryptAndDecompress([]byte("short"))
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext for short data, got: %v", err)
	}
}

func TestCrypto_TamperedData(t *testing.T) {
	c, err := New("test-secret-key-for-encryption-32-bytes!")
	if err != nil {
		t.Fatalf("Failed to create crypto: %v", err)
	}
	defer c.Close()

	testData := []byte("secret message")

	// 加密
	encrypted, err := c.EncryptAndCompress(testData)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}

	// 篡改数据
	encrypted[len(encrypted)-1] ^= 0xFF

	// 解密应该失败
	_, err = c.DecryptAndDecompress(encrypted)
	if err == nil {
		t.Error("Expected decryption to fail for tampered data")
	}
}

func TestCompressionLevel(t *testing.T) {
	c, err := New("test-secret-key-for-encryption-32-bytes!")
	if err != nil {
		t.Fatalf("Failed to create crypto: %v", err)
	}
	defer c.Close()

	// 测试默认级别
	if c.GetCompressionLevel() != DefaultCompressionLevel {
		t.Errorf("Expected default level %d, got %d", DefaultCompressionLevel, c.GetCompressionLevel())
	}

	// 测试设置级别
	c.SetCompressionLevel(10)
	if c.GetCompressionLevel() != 10 {
		t.Errorf("Expected level 10, got %d", c.GetCompressionLevel())
	}

	// 测试边界
	c.SetCompressionLevel(0)  // 应该被修正为默认值
	if c.GetCompressionLevel() != DefaultCompressionLevel {
		t.Errorf("Expected default level for invalid input, got %d", c.GetCompressionLevel())
	}

	c.SetCompressionLevel(30)  // 应该被修正为默认值
	if c.GetCompressionLevel() != DefaultCompressionLevel {
		t.Errorf("Expected default level for invalid input, got %d", c.GetCompressionLevel())
	}
}

func TestAdjustCompressionLevel(t *testing.T) {
	c, err := New("test-secret-key-for-encryption-32-bytes!")
	if err != nil {
		t.Fatalf("Failed to create crypto: %v", err)
	}
	defer c.Close()

	// 低负载
	c.AdjustCompressionLevel(0.5)
	if c.GetCompressionLevel() != LowLoadCompressionLevel {
		t.Errorf("Expected low load level %d for 50%% CPU, got %d", LowLoadCompressionLevel, c.GetCompressionLevel())
	}

	// 高负载
	c.AdjustCompressionLevel(0.8)
	if c.GetCompressionLevel() != HighLoadCompressionLevel {
		t.Errorf("Expected high load level %d for 80%% CPU, got %d", HighLoadCompressionLevel, c.GetCompressionLevel())
	}
}

func BenchmarkEncryptAndCompress(b *testing.B) {
	c, _ := New("benchmark-key-32-bytes-long!!")
	defer c.Close()

	testData := []byte(`{"model":"llama2","messages":[{"role":"user","content":"Hello world, this is a test message for benchmarking the compression and encryption system"}],"stream":true}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.EncryptAndCompress(testData)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecryptAndDecompress(b *testing.B) {
	c, _ := New("benchmark-key-32-bytes-long!!")
	defer c.Close()

	testData := []byte(`{"model":"llama2","messages":[{"role":"user","content":"Hello world, this is a test message for benchmarking the compression and encryption system"}],"stream":true}`)
	encrypted, _ := c.EncryptAndCompress(testData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.DecryptAndDecompress(encrypted)
		if err != nil {
			b.Fatal(err)
		}
	}
}
