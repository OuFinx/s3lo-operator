package proxy

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	cosignsig "github.com/sigstore/cosign/v2/pkg/signature"
	sigsig "github.com/sigstore/sigstore/pkg/signature"
	_ "github.com/sigstore/sigstore/pkg/signature/kms/aws" // register AWS KMS provider

	"github.com/OuFinx/s3lo/pkg/storage"
)

// signatureRecord mirrors the JSON stored by s3lo sign at
// manifests/<image>/<ref>/signatures/<slug>.json.
type signatureRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Digest        string `json:"digest"`
	Signature     string `json:"signature"`
	Payload       string `json:"payload"`
}

// ErrNotSigned is returned when no signature file exists for the image.
var ErrNotSigned = errors.New("image has no signature")

// ErrDigestMismatch is returned when the signed digest does not match the manifest being served.
var ErrDigestMismatch = errors.New("signed digest does not match manifest")

// ErrInvalidSignature is returned when the cryptographic signature fails verification.
var ErrInvalidSignature = errors.New("invalid signature")

// Verifier loads a public or KMS key at startup and checks manifest signatures on request.
type Verifier struct {
	verifier sigsig.Verifier
	slug     string   // 16-char hex key ID derived from public key; used to find signature files
	cache    sync.Map // verified manifest digest (string) → struct{}
}

// NewVerifier loads a cosign verifier from keyRef.
// keyRef may be a PEM public key file path, awskms:// ARN, gcpkms:// URL, etc.
func NewVerifier(ctx context.Context, keyRef string) (*Verifier, error) {
	v, err := cosignsig.VerifierForKeyRef(ctx, keyRef, crypto.SHA256)
	if err != nil {
		// Fall back to SignerVerifierFromKeyRef for private-key files (.key).
		sv, svErr := cosignsig.SignerVerifierFromKeyRef(ctx, keyRef, func(_ bool) ([]byte, error) {
			return []byte(os.Getenv("COSIGN_PASSWORD")), nil
		}, nil)
		if svErr != nil {
			return nil, fmt.Errorf("load key %q: %w", keyRef, err)
		}
		v = sv
	}
	return newVerifierFromCosign(v)
}

// newVerifierFromCosign creates a Verifier from a pre-loaded cosign/sigstore verifier.
// Used in tests to inject a verifier without loading from a file.
func newVerifierFromCosign(v sigsig.Verifier) (*Verifier, error) {
	pub, err := v.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}
	slug, err := keyIDSlug(pub)
	if err != nil {
		return nil, fmt.Errorf("derive key ID: %w", err)
	}
	return &Verifier{verifier: v, slug: slug}, nil
}

// objectGetter can fetch a single object from storage.
type objectGetter interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
}

// Check verifies the signature of manifestData for the given image/ref in bucket.
// Returns nil when the signature is valid.
// Returns ErrNotSigned, ErrDigestMismatch, or ErrInvalidSignature for policy failures.
// Returns a wrapped infrastructure error for storage or parse failures.
func (v *Verifier) Check(ctx context.Context, s objectGetter, bucket, image, ref string, manifestData []byte) error {
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifestData))

	if _, ok := v.cache.Load(digest); ok {
		return nil
	}

	sigKey := "manifests/" + image + "/" + ref + "/signatures/" + v.slug + ".json"
	sigData, err := s.GetObject(ctx, bucket, sigKey)
	if err != nil {
		if storage.IsNotFound(err) {
			return fmt.Errorf("%w: %s", ErrNotSigned, sigKey)
		}
		return fmt.Errorf("read signature: %w", err)
	}

	var rec signatureRecord
	if err := json.Unmarshal(sigData, &rec); err != nil {
		return fmt.Errorf("parse signature record: %w", err)
	}

	if rec.Digest != digest {
		return fmt.Errorf("%w: signed=%s current=%s", ErrDigestMismatch, rec.Digest, digest)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(rec.Signature)
	if err != nil {
		return fmt.Errorf("decode signature bytes: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(rec.Payload)
	if err != nil {
		return fmt.Errorf("decode payload bytes: %w", err)
	}

	if err := v.verifier.VerifySignature(bytes.NewReader(sigBytes), bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	v.cache.Store(digest, struct{}{})
	return nil
}

// keyIDSlug returns a 16-char hex key identifier derived from SHA-256 of the DER-encoded public key.
func keyIDSlug(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16], nil
}

