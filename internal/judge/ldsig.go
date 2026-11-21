package judge

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"net/url"
	"time"

	"github.com/piprate/json-gold/ld"
)

// LD signatures (RsaSignature2017) sign a JSON-LD document itself, so that
// a server can forward it and the receiver still knows its author. Mastodon
// and the original (app/ldsig.py) make them alike: an RSA-SHA256 signature
// over two hex SHA-256 hashes, of the signature's options and of the document
// without its signature, each normalized with URDNA2015 to N-Quads.

// ldOptionsContext is the context the options are normalized in. Mastodon
// uses identity/v1 and the original security/v1; both define creator and
// created alike, so the hashes agree.
const ldOptionsContext = "https://w3id.org/identity/v1"

// ldSign signs doc as keyID's owner at created, replacing any signature it
// has, the way Mastodon does.
func ldSign(doc map[string]any, keyID string, created time.Time, key *rsa.PrivateKey) error {
	sig := map[string]any{"type": "RsaSignature2017", "creator": keyID, "created": created.UTC().Format(time.RFC3339)}
	doc["signature"] = sig
	digest, err := ldDigest(doc)
	if err != nil {
		return err
	}
	value, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest)
	if err != nil {
		return err
	}
	sig["signatureValue"] = base64.StdEncoding.EncodeToString(value)
	return nil
}

// ldVerify checks doc's LD signature with key.
func ldVerify(doc map[string]any, key *rsa.PublicKey) error {
	sig, ok := doc["signature"].(map[string]any)
	if !ok {
		return fmt.Errorf("no signature")
	}
	value, ok := sig["signatureValue"].(string)
	if !ok {
		return fmt.Errorf("signature without a value: %v", sig)
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	digest, err := ldDigest(doc)
	if err != nil {
		return err
	}
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest, raw)
}

// ldDigest is the SHA-256 of what an LD signature signs.
func ldDigest(doc map[string]any) ([]byte, error) {
	sig, ok := doc["signature"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no signature")
	}
	options := maps.Clone(sig)
	for _, k := range []string{"type", "id", "signatureValue"} {
		delete(options, k)
	}
	options["@context"] = ldOptionsContext
	optionsHash, err := ldHash(options)
	if err != nil {
		return nil, fmt.Errorf("hashing the signature's options: %w", err)
	}
	unsigned := maps.Clone(doc)
	delete(unsigned, "signature")
	docHash, err := ldHash(unsigned)
	if err != nil {
		return nil, fmt.Errorf("hashing the document: %w", err)
	}
	sum := sha256.Sum256([]byte(optionsHash + docHash))
	return sum[:], nil
}

// ldHash is the hex SHA-256 of doc normalized with URDNA2015.
func ldHash(doc map[string]any) (string, error) {
	opts := ld.NewJsonLdOptions("")
	opts.Algorithm = ld.AlgorithmURDNA2015
	opts.Format = "application/n-quads"
	opts.DocumentLoader = capturedContexts{}
	normalized, err := ld.NewJsonLdProcessor().Normalize(doc, opts)
	if err != nil {
		return "", err
	}
	quads, ok := normalized.(string)
	if !ok {
		return "", fmt.Errorf("normalizing gave %T, not N-Quads", normalized)
	}
	sum := sha256.Sum256([]byte(quads))
	return hex.EncodeToString(sum[:]), nil
}

// capturedContexts loads JSON-LD contexts from the captured copies the
// proxy serves (jsonld.go), never from the web.
type capturedContexts struct{}

func (capturedContexts) LoadDocument(u string) (*ld.RemoteDocument, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	name, ok := jsonLDContexts[parsed.Host][parsed.Path]
	if !ok {
		return nil, fmt.Errorf("JSON-LD context %s isn't captured", u)
	}
	b, err := jsonld.ReadFile("testdata/jsonld/" + name)
	if err != nil {
		return nil, err
	}
	doc, err := ld.DocumentFromReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return &ld.RemoteDocument{DocumentURL: u, Document: doc}, nil
}
