package temporal

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	namespacev1 "go.temporal.io/cloud-sdk/api/namespace/v1"
)

func TestRetentionDays(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want int32
	}{
		{0, 1},
		{time.Hour, 1},
		{24 * time.Hour, 1},
		{36 * time.Hour, 2},
		{72 * time.Hour, 3},
		{200 * 24 * time.Hour, 90},
	} {
		if got := retentionDays(tc.in); got != tc.want {
			t.Errorf("retentionDays(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestClientNamespace(t *testing.T) {
	spec := func(filters ...string) *namespacev1.NamespaceSpec {
		s := &namespacev1.NamespaceSpec{MtlsAuth: &namespacev1.MtlsAuthSpec{}}
		for _, f := range filters {
			s.MtlsAuth.CertificateFilters = append(s.MtlsAuth.CertificateFilters, &namespacev1.CertificateFilterSpec{SubjectAlternativeName: f})
		}
		return s
	}
	for _, tc := range []struct {
		name string
		spec *namespacev1.NamespaceSpec
		want string
	}{
		{"own filter", spec(clientFilter("homelab.local", "foo")), "foo"},
		{"other trust domain", spec(clientFilter("example.org", "foo")), ""},
		{"one service account", spec("spiffe://homelab.local/ns/foo/sa/bar"), ""},
		{"nested path", spec("spiffe://homelab.local/ns/foo/sa/x/ns/bar/sa/*"), ""},
		{"two filters", spec(clientFilter("homelab.local", "foo"), clientFilter("homelab.local", "bar")), ""},
		{"no filter", spec(), ""},
	} {
		if got := clientNamespace(tc.spec, "homelab.local"); got != tc.want {
			t.Errorf("%s: clientNamespace = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAgainstTemporalCloud creates, changes and deletes a real Temporal Cloud namespace.
// TEMPORAL_CLOUD_API_KEY_FILE: set it to a file holding a key with the Developer role, or the test skips.
// It costs a few cents and takes about 3 minutes.
func TestAgainstTemporalCloud(t *testing.T) {
	keyFile := os.Getenv("TEMPORAL_CLOUD_API_KEY_FILE")
	if keyFile == "" {
		t.Skip("TEMPORAL_CLOUD_API_KEY_FILE not set")
	}
	ca := testCA(t)
	c, err := NewCloud(keyFile, func(context.Context) (Trust, error) { return Trust{CA: ca, Domain: "homelab.local"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	name := fmt.Sprintf("tc-it-%d", time.Now().Unix())

	if got, err := c.Describe(ctx, name); err != nil || got != nil {
		t.Fatalf("Describe before Register = %v, %v, want nil, nil", got, err)
	}

	want := Namespace{
		Name:            name,
		Retention:       24 * time.Hour,
		Owner:           "default/foo",
		ClientNamespace: "default",
	}
	if err := c.Register(ctx, want); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), name) })

	got := waitActive(ctx, t, c, name)
	if got.Owner != want.Owner || got.ClientNamespace != want.ClientNamespace || got.Retention != want.Retention {
		t.Errorf("after Register = %+v, want owner, client ID and retention from %+v", got, want)
	}
	if got.FullName == "" || got.Endpoint == "" || got.ClientCAStale {
		t.Errorf("after Register = %+v, want a full name, an endpoint and a current CA", got)
	}

	want.Retention = 48 * time.Hour
	if err := c.Update(ctx, want); err != nil {
		t.Fatal(err)
	}
	// The update runs in the background, and the namespace can still read as active before it starts.
	for waitActive(ctx, t, c, name).Retention != want.Retention {
		sleep(ctx, t)
	}

	if err := c.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	for {
		got, err := c.Describe(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			return
		}
		sleep(ctx, t)
	}
}

func waitActive(ctx context.Context, t *testing.T, c *Cloud, name string) *Namespace {
	t.Helper()
	for {
		got, err := c.Describe(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil && !got.Pending {
			return got
		}
		sleep(ctx, t)
	}
}

func sleep(ctx context.Context, t *testing.T) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(10 * time.Second):
	}
}

// testCA stands in for the SPIRE trust bundle.
func testCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "temporal-controller-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
