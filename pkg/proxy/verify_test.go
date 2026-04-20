package proxy

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	sigsig "github.com/sigstore/sigstore/pkg/signature"
)

// ecdsaTestVerifier implements sigsig.Verifier using a standard ecdsa key.
type ecdsaTestVerifier struct {
	pub *ecdsa.PublicKey
}

func (v *ecdsaTestVerifier) VerifySignature(sig, msg io.Reader, _ ...sigsig.VerifyOption) error {
	sigBytes, err := io.ReadAll(sig)
	if err != nil {
		return err
	}
	msgBytes, err := io.ReadAll(msg)
	if err != nil {
		return err
	}
	h := sha256.Sum256(msgBytes)
	if !ecdsa.VerifyASN1(v.pub, h[:], sigBytes) {
		return fmt.Errorf("ecdsa: verification failed")
	}
	return nil
}

func (v *ecdsaTestVerifier) PublicKey(_ ...sigsig.PublicKeyOption) (crypto.PublicKey, error) {
	return v.pub, nil
}

// makeTestKey returns a fresh ECDSA P-256 key pair.
func makeTestKey(t *testing.T) (*ecdsa.PrivateKey, *ecdsa.PublicKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv, &priv.PublicKey
}

// signPayload signs payload bytes with privKey using ASN.1 ECDSA + SHA256 — matching cosign format.
func signPayload(t *testing.T, priv *ecdsa.PrivateKey, payload []byte) []byte {
	t.Helper()
	h := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, h[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

// manifestDigest computes the sha256:<hex> digest string for manifestData.
func manifestDigest(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h)
}

// makeVerifier creates a Verifier using the test public key.
func makeVerifier(t *testing.T, pub *ecdsa.PublicKey) *Verifier {
	t.Helper()
	v, err := newVerifierFromCosign(&ecdsaTestVerifier{pub: pub})
	if err != nil {
		t.Fatalf("newVerifierFromCosign: %v", err)
	}
	return v
}

// putSignature stores a valid signature record in fakeStorage for the given image/ref/manifestData.
func putSignature(t *testing.T, s *fakeStorage, v *Verifier, priv *ecdsa.PrivateKey, bucket, image, ref string, manifestData []byte) {
	t.Helper()
	digest := manifestDigest(manifestData)
	payload := []byte(digest + "\n")
	sig := signPayload(t, priv, payload)

	rec := signatureRecord{
		SchemaVersion: 1,
		Digest:        digest,
		Signature:     base64.StdEncoding.EncodeToString(sig),
		Payload:       base64.StdEncoding.EncodeToString(payload),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	sigKey := "manifests/" + image + "/" + ref + "/signatures/" + v.slug + ".json"
	s.put(bucket, sigKey, data)
}

func TestVerifier_ValidSignature(t *testing.T) {
	priv, pub := makeTestKey(t)
	s := newFakeStorage()
	v := makeVerifier(t, pub)
	manifest := singleManifestJSON

	putSignature(t, s, v, priv, "my-bucket", "myapp", "v1.0", manifest)

	err := v.Check(context.Background(), s, "my-bucket", "myapp", "v1.0", manifest)
	if err != nil {
		t.Errorf("expected valid signature, got: %v", err)
	}
}

func TestVerifier_CachesResult(t *testing.T) {
	priv, pub := makeTestKey(t)
	s := newFakeStorage()
	v := makeVerifier(t, pub)
	manifest := singleManifestJSON

	putSignature(t, s, v, priv, "my-bucket", "myapp", "v1.0", manifest)

	// First call: hits storage
	if err := v.Check(context.Background(), s, "my-bucket", "myapp", "v1.0", manifest); err != nil {
		t.Fatalf("first check: %v", err)
	}

	// Remove signature from storage — second call must use cache
	delete(s.objects, "my-bucket/manifests/myapp/v1.0/signatures/"+v.slug+".json")

	if err := v.Check(context.Background(), s, "my-bucket", "myapp", "v1.0", manifest); err != nil {
		t.Errorf("cached check should not hit storage, got: %v", err)
	}
}

func TestVerifier_NotSigned(t *testing.T) {
	_, pub := makeTestKey(t)
	v := makeVerifier(t, pub)

	err := v.Check(context.Background(), newFakeStorage(), "my-bucket", "myapp", "v1.0", singleManifestJSON)
	if err == nil {
		t.Fatal("expected error for unsigned image")
	}
	if !errors.Is(err, ErrNotSigned) {
		t.Errorf("expected ErrNotSigned, got: %v", err)
	}
}

func TestVerifier_DigestMismatch(t *testing.T) {
	priv, pub := makeTestKey(t)
	s := newFakeStorage()
	v := makeVerifier(t, pub)

	// Sign original manifest, then serve a different one
	putSignature(t, s, v, priv, "my-bucket", "myapp", "v1.0", singleManifestJSON)
	tamperedManifest := []byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","schemaVersion":2,"tampered":true}`)

	err := v.Check(context.Background(), s, "my-bucket", "myapp", "v1.0", tamperedManifest)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Errorf("expected ErrDigestMismatch, got: %v", err)
	}
}

func TestVerifier_InvalidSignature(t *testing.T) {
	_, pub := makeTestKey(t)
	s := newFakeStorage()
	v := makeVerifier(t, pub)
	manifest := singleManifestJSON
	digest := manifestDigest(manifest)

	// Store a record with a bad (random) signature
	rec := signatureRecord{
		SchemaVersion: 1,
		Digest:        digest,
		Signature:     base64.StdEncoding.EncodeToString([]byte("badsig")),
		Payload:       base64.StdEncoding.EncodeToString([]byte(digest + "\n")),
	}
	data, _ := json.Marshal(rec)
	sigKey := "manifests/myapp/v1.0/signatures/" + v.slug + ".json"
	s.put("my-bucket", sigKey, data)

	err := v.Check(context.Background(), s, "my-bucket", "myapp", "v1.0", manifest)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

func TestKeyIDSlug_Deterministic(t *testing.T) {
	_, pub := makeTestKey(t)
	slug1, err := keyIDSlug(pub)
	if err != nil {
		t.Fatalf("keyIDSlug: %v", err)
	}
	slug2, err := keyIDSlug(pub)
	if err != nil {
		t.Fatalf("keyIDSlug second call: %v", err)
	}
	if slug1 != slug2 {
		t.Errorf("keyIDSlug not deterministic: %q vs %q", slug1, slug2)
	}
	if len(slug1) != 16 {
		t.Errorf("slug length = %d, want 16", len(slug1))
	}
}

func TestKeyIDSlug_DifferentKeys(t *testing.T) {
	_, pub1 := makeTestKey(t)
	_, pub2 := makeTestKey(t)
	slug1, _ := keyIDSlug(pub1)
	slug2, _ := keyIDSlug(pub2)
	if slug1 == slug2 {
		t.Error("different keys must produce different slugs")
	}
}

func TestVerifier_PublicKeyMarshal(t *testing.T) {
	_, pub := makeTestKey(t)
	// x509.MarshalPKIXPublicKey should work for ecdsa keys
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(der) == 0 {
		t.Error("empty DER encoding")
	}
}

var _ sigsig.Verifier = (*ecdsaTestVerifier)(nil) // compile-time interface check

// Silence "bytes imported and not used" in case the test file is compiled standalone.
var _ = bytes.NewReader
