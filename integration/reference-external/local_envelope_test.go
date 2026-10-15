package referenceexternal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"github.com/faustbrian/go-filesystem/v2/local"
	secretenvelope "github.com/faustbrian/go-secret-envelope/v2"
	"github.com/faustbrian/go-secret-envelope/v2/adapters/keyring"
	referenceexternal "github.com/faustbrian/go-service/integration/reference-external"
)

// This is local adapter/service reconstruction with caller-owned keys, not automatic
// rotation, a power-loss experiment, or evidence for a cloud key/storage policy.
func TestReferenceLocalEnvelopeReopensWithOverlappingKeys(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	oldKeys := map[string][]byte{"old": bytes.Repeat([]byte{0x41}, secretenvelope.DataKeySize)}
	overlap := map[string][]byte{"old": oldKeys["old"], "new": bytes.Repeat([]byte{0x42}, secretenvelope.DataKeySize)}
	oldObject, newObject := filesystem.MustParsePath("secrets/old.bin"), filesystem.MustParsePath("secrets/new.bin")
	oldPlaintext, newPlaintext := []byte("old-local-test-value"), []byte("new-local-test-value")
	binding, err := secretenvelope.NewContext(map[string]string{"service": "reference-external", "record": "vendor"})
	if err != nil {
		t.Fatal(err)
	}
	_, reference, closeOld := openLocalEnvelopeReference(t, root, oldKeys)
	if err := reference.StoreSecret(t.Context(), oldObject, oldPlaintext, "old", binding); err != nil {
		t.Fatal(err)
	}
	oldEncoded := assertStoredEnvelope(t, reference, oldObject, "old", oldPlaintext)
	closeOld()

	_, reference, closeOverlap := openLocalEnvelopeReference(t, root, overlap)
	assertLoadedSecret(t, reference, oldObject, binding, oldPlaintext)
	if err := reference.StoreSecret(t.Context(), newObject, newPlaintext, "new", binding); err != nil {
		t.Fatal(err)
	}
	assertStoredEnvelope(t, reference, newObject, "new", newPlaintext)
	closeOverlap()

	_, reference, closeNewOnly := openLocalEnvelopeReference(t, root, map[string][]byte{"new": overlap["new"]})
	if plaintext, err := reference.LoadSecret(t.Context(), oldObject, binding); !errors.Is(err, secretenvelope.ErrKeyProvider) || len(plaintext) != 0 {
		t.Fatal("missing old key did not fail closed")
	}
	assertLoadedSecret(t, reference, newObject, binding, newPlaintext)
	closeNewOnly()

	storage, reference, closeReconstructed := openLocalEnvelopeReference(t, root, overlap)
	assertLoadedSecret(t, reference, oldObject, binding, oldPlaintext)
	assertLoadedSecret(t, reference, newObject, binding, newPlaintext)
	wrongBinding, err := secretenvelope.NewContext(map[string]string{"service": "reference-external", "record": "other"})
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := reference.LoadSecret(t.Context(), oldObject, wrongBinding); err == nil || len(plaintext) != 0 {
		t.Fatal("wrong binding did not fail closed")
	}
	tampered := bytes.Clone(oldEncoded)
	tampered[len(tampered)-1] ^= 1
	tamperedObject := filesystem.MustParsePath("secrets/tampered.bin")
	if _, err := storage.Write(t.Context(), tamperedObject, bytes.NewReader(tampered), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if plaintext, err := reference.LoadSecret(t.Context(), tamperedObject, binding); !errors.Is(err, secretenvelope.ErrAuthentication) || len(plaintext) != 0 {
		t.Fatal("tampered ciphertext did not fail closed")
	}

	// Exercise the borrowed adapter's public streaming source boundary: some
	// replacement bytes arrive, then the source fails before atomic publication.
	sourceFailure := errors.New("test source interrupted")
	source := &interruptedEnvelopeSource{prefix: bytes.NewReader(tampered[:len(tampered)/2]), err: sourceFailure}
	if _, err := storage.Write(t.Context(), oldObject, source, filesystem.WriteOptions{}); !errors.Is(err, sourceFailure) || source.read == 0 {
		t.Fatal("overwrite did not exercise a partial failing source")
	}
	closeReconstructed()
	_, reference, _ = openLocalEnvelopeReference(t, root, overlap)
	if retained := assertStoredEnvelope(t, reference, oldObject, "old", oldPlaintext); !bytes.Equal(retained, oldEncoded) {
		t.Fatal("failed partial overwrite changed the published envelope")
	}
	assertLoadedSecret(t, reference, oldObject, binding, oldPlaintext)
	assertLoadedSecret(t, reference, newObject, binding, newPlaintext)
}

type interruptedEnvelopeSource struct {
	prefix *bytes.Reader
	err    error
	read   int
}

func (source *interruptedEnvelopeSource) Read(buffer []byte) (int, error) {
	n, err := source.prefix.Read(buffer)
	source.read += n
	if errors.Is(err, io.EOF) {
		return n, source.err
	}
	return n, err
}

func openLocalEnvelopeReference(t *testing.T, root string, keys map[string][]byte) (*local.Adapter, *referenceexternal.Reference, func()) {
	t.Helper()
	storage, err := local.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	// The adapter belongs to this caller, including on later construction failure.
	var closeStorage sync.Once
	closeAdapter := func() {
		closeStorage.Do(func() {
			if err := storage.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(closeAdapter)
	provider, err := keyring.New(keys)
	if err != nil {
		t.Fatal(err)
	}
	envelopes, err := secretenvelope.NewService(provider)
	if err != nil {
		t.Fatal(err)
	}
	config := validConfig(t, "http://127.0.0.1", storage)
	config.Envelopes = envelopes
	reference, err := referenceexternal.New(config)
	if err != nil {
		t.Fatal(err)
	}
	var closeReference sync.Once
	closeAll := func() {
		closeReference.Do(func() {
			if err := reference.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		closeAdapter()
	}
	t.Cleanup(closeAll)
	return storage, reference, closeAll
}

func assertStoredEnvelope(t *testing.T, reference *referenceexternal.Reference, object filesystem.Path, key string, plaintext []byte) []byte {
	t.Helper()
	encoded, err := reference.OpenStored(t.Context(), object)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := secretenvelope.ParseEnvelope(encoded)
	if err != nil || envelope.KeyReference() != key || bytes.Contains(encoded, plaintext) {
		t.Fatal("stored bytes lost the encrypted envelope/key-reference contract")
	}
	return encoded
}

func assertLoadedSecret(t *testing.T, reference *referenceexternal.Reference, object filesystem.Path, binding secretenvelope.Context, want []byte) {
	t.Helper()
	got, err := reference.LoadSecret(t.Context(), object, binding)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("reconstructed secret did not round-trip: %v", err)
	}
}
