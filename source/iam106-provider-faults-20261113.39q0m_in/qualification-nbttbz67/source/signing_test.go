package boundary

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestSigningFileAndPublishedKeyBinding(t *testing.T) {
	folder := t.TempDir()
	if os.Chmod(folder, 0700) != nil {
		t.Fatal("owned signing fixture directory unavailable")
	}
	privatePath, publicPath, foreignPath := filepath.Join(folder, "owned-signing.pem"), filepath.Join(folder, "owned-public.pem"), filepath.Join(folder, "owned-other-public.pem")
	signing, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("owned signing key unavailable")
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(signing)
	if err != nil {
		t.Fatal("owned signing key encoding failed")
	}
	privateWrite(t, privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
	clear(encoded)
	public, err := x509.MarshalPKIXPublicKey(&signing.PublicKey)
	if err != nil {
		t.Fatal("owned public key encoding failed")
	}
	privateWrite(t, publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	foreign, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("owned second key unavailable")
	}
	other, err := x509.MarshalPKIXPublicKey(&foreign.PublicKey)
	if err != nil {
		t.Fatal("owned second public key encoding failed")
	}
	privateWrite(t, foreignPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: other}))
	store := &sdkStore{SigningFile: privatePath, signingID: "active", publicFiles: map[string]string{"active": publicPath}}
	for _, name := range []string{"missing_active_id", "mismatched_active_key", "retained_key_cannot_mask_mismatch", "changed_public_file", "matching_pair"} {
		t.Run(name, func(t *testing.T) {
			store.publicFiles = map[string]string{"active": publicPath}
			switch name {
			case "missing_active_id":
				store.publicFiles = map[string]string{"previous": publicPath}
			case "mismatched_active_key":
				store.publicFiles["active"] = foreignPath
			case "retained_key_cannot_mask_mismatch":
				store.publicFiles = map[string]string{"previous": publicPath, "active": foreignPath}
			case "changed_public_file":
				privateWrite(t, publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: other}))
				defer privateWrite(t, publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
			case "matching_pair":
				store.publicFiles["previous"] = foreignPath
			}
			key, err := store.SigningKey(context.Background())
			if name == "matching_pair" {
				if err != nil || key.ID() != "active" {
					t.Fatal("matching active pair was refused")
				}
			} else if err == nil || key != nil {
				t.Fatal("missing or mismatched active signing key accepted")
			}
		})
	}
}
