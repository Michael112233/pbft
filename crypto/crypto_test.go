package crypto

import (
	"path/filepath"
	"testing"
)

func TestECDSAPEMRoundTrip(t *testing.T) {
	privateKey, err := GenerateP256Key()
	if err != nil {
		t.Fatalf("GenerateP256Key() error = %v", err)
	}

	privatePath := filepath.Join(t.TempDir(), "vrf_priv.pem")
	publicPath := filepath.Join(t.TempDir(), "vrf_pub.pem")

	if err := SaveECDSAPrivateKey(privatePath, privateKey); err != nil {
		t.Fatalf("SaveECDSAPrivateKey() error = %v", err)
	}
	if err := SaveECDSAPublicKey(publicPath, &privateKey.PublicKey); err != nil {
		t.Fatalf("SaveECDSAPublicKey() error = %v", err)
	}

	readPrivateKey, err := ReadECDSAPrivateKey(privatePath)
	if err != nil {
		t.Fatalf("ReadECDSAPrivateKey() error = %v", err)
	}
	readPublicKey, err := ReadECDSAPublicKey(publicPath)
	if err != nil {
		t.Fatalf("ReadECDSAPublicKey() error = %v", err)
	}

	if privateKey.D.Cmp(readPrivateKey.D) != 0 {
		t.Fatal("private key changed after PEM round trip")
	}
	if privateKey.X.Cmp(readPublicKey.X) != 0 || privateKey.Y.Cmp(readPublicKey.Y) != 0 {
		t.Fatal("public key changed after PEM round trip")
	}
}
